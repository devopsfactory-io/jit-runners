// Package ec2 is the AWS EC2 implementation of the compute.Launcher contract
// defined in internal/compute.
package ec2

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
)

// Tag keys applied to launched instances.
const (
	tagManagedBy   = "managed-by"
	tagManagedVal  = "jit-runners"
	tagRunnerIDKey = "jit-runners-id"
)

// API abstracts the EC2 RunInstances API for testing.
type API interface {
	RunInstances(ctx context.Context, input *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(ctx context.Context, input *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// LauncherOptions configures AWS-specific launch parameters that are not part
// of the cloud-agnostic compute.LaunchSpec.
type LauncherOptions struct {
	SecurityGroupID    string
	IAMInstanceProfile string
	SpotMaxPrice       string // empty = on-demand price cap
	CpuCredits         string // "standard" pins burstable (t-family) launches; "" = AWS default (unlimited)
	ExtraTags          map[string]string
}

// Launcher manages EC2 instance lifecycle for runners. It satisfies
// compute.Launcher.
type Launcher struct {
	client API
	opts   LauncherOptions
}

// Compile-time assertion that Launcher satisfies compute.Launcher.
var _ compute.Launcher = (*Launcher)(nil)

// NewLauncher creates a Launcher with the given EC2 client and AWS-specific
// options.
func NewLauncher(client API, opts LauncherOptions) *Launcher {
	return &Launcher{client: client, opts: opts}
}

// Launch diversifies across the ordered candidate instance types and subnets,
// requesting spot capacity first-success-wins. It aborts early only on a closed
// allow-list of fatal request errors (isFatal); every other error — including
// unknown API codes and transport errors — is treated as retryable so the loop
// continues, preserving the "launch success ≥ today" guarantee. If no spot
// attempt succeeds, it makes one on-demand last-resort attempt on the primary
// candidate/subnet. Returns the resulting Instance.
func (l *Launcher) Launch(ctx context.Context, spec compute.LaunchSpec) (compute.Instance, error) {
	if len(spec.InstanceTypes) == 0 {
		return compute.Instance{}, fmt.Errorf("launch: spec has no instance types")
	}
	subnets := spec.SubnetIDs
	if len(subnets) == 0 {
		subnets = []string{""} // "" → EC2 picks a default-VPC subnet (today's behavior)
	}
	subnets = rotate(subnets, spec.RunnerID)

	var lastErr error
	for _, it := range spec.InstanceTypes {
		for _, sn := range subnets {
			id, err := l.runInstance(ctx, spec, it, sn, true)
			if err == nil {
				return newInstance(id, spec), nil
			}
			if isFatal(err) {
				return compute.Instance{}, fmt.Errorf("launch aborted (fatal): %w", err)
			}
			lastErr = err
		}
	}
	log.Printf("event=spot_exhausted_ondemand_fallback runner=%s attempted_types=%d", spec.RunnerID, len(spec.InstanceTypes))
	id, err := l.runInstance(ctx, spec, spec.InstanceTypes[0], subnets[0], false)
	if err != nil {
		return compute.Instance{}, fmt.Errorf("spot and on-demand launch both failed (spot: %v): %w", lastErr, err)
	}
	return newInstance(id, spec), nil
}

func newInstance(id string, spec compute.LaunchSpec) compute.Instance {
	return compute.Instance{ID: id, State: "pending", LaunchedAt: time.Now().UTC(), RunnerID: spec.RunnerID}
}

// rotate offsets the subnet order deterministically by the runner ID so
// concurrent launches spread across AZs instead of all starting at subnets[0].
func rotate(subnets []string, seed string) []string {
	if len(subnets) <= 1 {
		return subnets
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	off := int(h.Sum32() % uint32(len(subnets)))
	out := make([]string, 0, len(subnets))
	out = append(out, subnets[off:]...)
	out = append(out, subnets[:off]...)
	return out
}

// isFatal reports whether err is a request-level error that would fail on
// on-demand too — the only case where skipping the on-demand fallback is safe.
// DEFAULT: unknown/non-API errors are retryable (return false), preserving the
// "launch success ≥ today" guarantee.
func isFatal(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	code := apiErr.ErrorCode()
	if strings.HasPrefix(code, "InvalidAMIID") {
		return true
	}
	switch code {
	case "UnauthorizedOperation", "AuthFailure", "PendingVerification",
		"InvalidParameterValue", "InvalidParameterCombination":
		return true
	}
	// Allow-list is intentionally minimal and grows only as production logs
	// reveal codes that also fail on-demand. Unknown ⇒ retryable (see doc above).
	return false
}

// isBurstable reports whether instanceType is a t-family (burstable) type.
func isBurstable(instanceType string) bool {
	for _, p := range []string{"t2.", "t3.", "t3a.", "t4g."} {
		if strings.HasPrefix(instanceType, p) {
			return true
		}
	}
	return false
}

// runInstance issues a single RunInstances call. If spot is true, market
// options are configured for spot pricing; otherwise the request is on-demand.
func (l *Launcher) runInstance(ctx context.Context, spec compute.LaunchSpec, instanceType, subnetID string, spot bool) (string, error) {
	tags := make([]types.Tag, 0, len(l.opts.ExtraTags)+2)
	tags = append(tags, types.Tag{
		Key:   aws.String(tagManagedBy),
		Value: aws.String(tagManagedVal),
	})
	if spec.RunnerID != "" {
		tags = append(tags, types.Tag{
			Key:   aws.String(tagRunnerIDKey),
			Value: aws.String(spec.RunnerID),
		})
	}
	for k, v := range l.opts.ExtraTags {
		tags = append(tags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(spec.ImageID),
		InstanceType: types.InstanceType(instanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		UserData:     aws.String(spec.UserData),
		TagSpecifications: []types.TagSpecification{
			{ResourceType: types.ResourceTypeInstance, Tags: tags},
		},
	}
	if l.opts.CpuCredits != "" && isBurstable(instanceType) {
		input.CreditSpecification = &types.CreditSpecificationRequest{
			CpuCredits: aws.String(l.opts.CpuCredits),
		}
	}
	if spot {
		input.InstanceMarketOptions = &types.InstanceMarketOptionsRequest{
			MarketType: types.MarketTypeSpot,
			SpotOptions: &types.SpotMarketOptions{
				InstanceInterruptionBehavior: types.InstanceInterruptionBehaviorTerminate,
			},
		}
		if l.opts.SpotMaxPrice != "" {
			input.InstanceMarketOptions.SpotOptions.MaxPrice = aws.String(l.opts.SpotMaxPrice)
		}
	}

	if subnetID != "" {
		input.SubnetId = aws.String(subnetID)
	}
	if l.opts.SecurityGroupID != "" {
		input.SecurityGroupIds = []string{l.opts.SecurityGroupID}
	}
	if l.opts.IAMInstanceProfile != "" {
		input.IamInstanceProfile = &types.IamInstanceProfileSpecification{
			Name: aws.String(l.opts.IAMInstanceProfile),
		}
	}

	out, err := l.client.RunInstances(ctx, input)
	if err != nil {
		variant := "spot"
		if !spot {
			variant = "on-demand"
		}
		return "", fmt.Errorf("run EC2 %s instance: %w", variant, err)
	}
	if len(out.Instances) == 0 {
		return "", fmt.Errorf("no instances returned from RunInstances")
	}
	return aws.ToString(out.Instances[0].InstanceId), nil
}

// Terminate stops the given EC2 instances. A nil/empty list is a no-op.
func (l *Launcher) Terminate(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := l.client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: ids,
	})
	if err != nil {
		return fmt.Errorf("terminate instances: %w", err)
	}
	return nil
}

// ListStale returns all running/pending instances tagged with
// managed-by=jit-runners that have been launched longer than threshold.
// A zero threshold returns all managed instances.
func (l *Launcher) ListStale(ctx context.Context, threshold time.Duration) ([]compute.Instance, error) {
	out, err := l.client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:" + tagManagedBy),
				Values: []string{tagManagedVal},
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running", "pending"},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("describe managed instances: %w", err)
	}
	cutoff := time.Time{}
	if threshold > 0 {
		cutoff = time.Now().Add(-threshold)
	}
	var instances []compute.Instance
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			launchedAt := time.Time{}
			if inst.LaunchTime != nil {
				launchedAt = inst.LaunchTime.UTC()
			}
			if !cutoff.IsZero() && !launchedAt.IsZero() && launchedAt.After(cutoff) {
				continue
			}
			instances = append(instances, compute.Instance{
				ID:         aws.ToString(inst.InstanceId),
				State:      string(inst.State.Name),
				LaunchedAt: launchedAt,
				RunnerID:   tagValue(inst.Tags, tagRunnerIDKey),
			})
		}
	}
	return instances, nil
}

func tagValue(tags []types.Tag, key string) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}
	return ""
}
