package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// EC2API abstracts the EC2 RunInstances API for testing.
type EC2API interface {
	RunInstances(ctx context.Context, input *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(ctx context.Context, input *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// Launcher manages EC2 instance lifecycle for runners.
type Launcher struct {
	client EC2API
}

// NewLauncher creates a Launcher with the given EC2 client.
func NewLauncher(client EC2API) *Launcher {
	return &Launcher{client: client}
}

// Launch starts an EC2 spot instance with the given configuration.
// Returns the instance ID on success.
func (l *Launcher) Launch(ctx context.Context, cfg *LaunchConfig) (string, error) {
	tags := make([]types.Tag, 0, len(cfg.Tags)+1)
	tags = append(tags, types.Tag{
		Key:   aws.String("managed-by"),
		Value: aws.String("jit-runners"),
	})
	for k, v := range cfg.Tags {
		tags = append(tags, types.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(cfg.AMI),
		InstanceType: types.InstanceType(cfg.InstanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		UserData:     aws.String(cfg.UserData),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeInstance,
				Tags:         tags,
			},
		},
		InstanceMarketOptions: &types.InstanceMarketOptionsRequest{
			MarketType: types.MarketTypeSpot,
			SpotOptions: &types.SpotMarketOptions{
				InstanceInterruptionBehavior: types.InstanceInterruptionBehaviorTerminate,
			},
		},
	}

	if cfg.SubnetID != "" {
		input.SubnetId = aws.String(cfg.SubnetID)
	}
	if cfg.SecurityGroupID != "" {
		input.SecurityGroupIds = []string{cfg.SecurityGroupID}
	}
	if cfg.IAMInstanceProfile != "" {
		input.IamInstanceProfile = &types.IamInstanceProfileSpecification{
			Name: aws.String(cfg.IAMInstanceProfile),
		}
	}
	if cfg.SpotMaxPrice != "" {
		input.InstanceMarketOptions.SpotOptions.MaxPrice = aws.String(cfg.SpotMaxPrice)
	}

	out, err := l.client.RunInstances(ctx, input)
	if err != nil {
		return "", fmt.Errorf("run EC2 instance: %w", err)
	}
	if len(out.Instances) == 0 {
		return "", fmt.Errorf("no instances returned from RunInstances")
	}
	return aws.ToString(out.Instances[0].InstanceId), nil
}

// LaunchOnDemand starts an on-demand EC2 instance (fallback when spot is unavailable).
func (l *Launcher) LaunchOnDemand(ctx context.Context, cfg *LaunchConfig) (string, error) {
	tags := make([]types.Tag, 0, len(cfg.Tags)+1)
	tags = append(tags, types.Tag{
		Key:   aws.String("managed-by"),
		Value: aws.String("jit-runners"),
	})
	for k, v := range cfg.Tags {
		tags = append(tags, types.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(cfg.AMI),
		InstanceType: types.InstanceType(cfg.InstanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		UserData:     aws.String(cfg.UserData),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeInstance,
				Tags:         tags,
			},
		},
	}

	if cfg.SubnetID != "" {
		input.SubnetId = aws.String(cfg.SubnetID)
	}
	if cfg.SecurityGroupID != "" {
		input.SecurityGroupIds = []string{cfg.SecurityGroupID}
	}
	if cfg.IAMInstanceProfile != "" {
		input.IamInstanceProfile = &types.IamInstanceProfileSpecification{
			Name: aws.String(cfg.IAMInstanceProfile),
		}
	}

	out, err := l.client.RunInstances(ctx, input)
	if err != nil {
		return "", fmt.Errorf("run EC2 on-demand instance: %w", err)
	}
	if len(out.Instances) == 0 {
		return "", fmt.Errorf("no instances returned from RunInstances")
	}
	return aws.ToString(out.Instances[0].InstanceId), nil
}

// Terminate stops the given EC2 instances.
func (l *Launcher) Terminate(ctx context.Context, instanceIDs ...string) error {
	if len(instanceIDs) == 0 {
		return nil
	}
	_, err := l.client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: instanceIDs,
	})
	if err != nil {
		return fmt.Errorf("terminate instances: %w", err)
	}
	return nil
}

// ListManagedInstances returns all running instances tagged with managed-by=jit-runners.
func (l *Launcher) ListManagedInstances(ctx context.Context) ([]string, error) {
	out, err := l.client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:managed-by"),
				Values: []string{"jit-runners"},
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
	var ids []string
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			ids = append(ids, aws.ToString(inst.InstanceId))
		}
	}
	return ids, nil
}
