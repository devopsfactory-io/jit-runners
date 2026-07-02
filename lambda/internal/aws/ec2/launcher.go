// Package ec2 is the AWS EC2 implementation of the compute.Launcher contract
// defined in internal/compute.
package ec2

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

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

// Launch starts an EC2 spot instance with the given spec, falling back to
// on-demand if the spot request fails. Returns the resulting Instance.
func (l *Launcher) Launch(ctx context.Context, spec compute.LaunchSpec) (compute.Instance, error) {
	if len(spec.InstanceTypes) == 0 {
		return compute.Instance{}, fmt.Errorf("launch: spec has no instance types")
	}
	it := spec.InstanceTypes[0]
	subnet := ""
	if len(spec.SubnetIDs) > 0 {
		subnet = spec.SubnetIDs[0]
	}
	id, err := l.runInstance(ctx, spec, it, subnet, true)
	if err != nil {
		idOD, errOD := l.runInstance(ctx, spec, it, subnet, false)
		if errOD != nil {
			return compute.Instance{}, fmt.Errorf("spot and on-demand launch both failed (spot: %v): %w", err, errOD)
		}
		id = idOD
	}
	return compute.Instance{ID: id, State: "pending", LaunchedAt: time.Now().UTC(), RunnerID: spec.RunnerID}, nil
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
