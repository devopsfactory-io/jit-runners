package ec2

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/compute"
)

type call struct {
	instanceType string
	subnet       string
	spot         bool
}

type fakeEC2 struct {
	errs  []error
	calls []call
}

func (f *fakeEC2) RunInstances(_ context.Context, in *awsec2.RunInstancesInput, _ ...func(*awsec2.Options)) (*awsec2.RunInstancesOutput, error) {
	c := call{instanceType: string(in.InstanceType), spot: in.InstanceMarketOptions != nil}
	if in.SubnetId != nil {
		c.subnet = *in.SubnetId
	}
	f.calls = append(f.calls, c)
	var err error
	if len(f.errs) > 0 {
		if len(f.calls)-1 < len(f.errs) {
			err = f.errs[len(f.calls)-1]
		} else {
			err = f.errs[len(f.errs)-1]
		}
	}
	if err != nil {
		return nil, err
	}
	return &awsec2.RunInstancesOutput{Instances: []types.Instance{{InstanceId: aws.String("i-abc")}}}, nil
}

func (f *fakeEC2) TerminateInstances(context.Context, *awsec2.TerminateInstancesInput, ...func(*awsec2.Options)) (*awsec2.TerminateInstancesOutput, error) {
	return &awsec2.TerminateInstancesOutput{}, nil
}
func (f *fakeEC2) DescribeInstances(context.Context, *awsec2.DescribeInstancesInput, ...func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
	return &awsec2.DescribeInstancesOutput{}, nil
}

func apiErr(code string) error { return &smithy.GenericAPIError{Code: code, Message: code} }

func spec(types_ []string, subnets []string) compute.LaunchSpec {
	return compute.LaunchSpec{InstanceTypes: types_, SubnetIDs: subnets, ImageID: "ami-1", RunnerID: "r1"}
}

func spotCalls(calls []call) (spot, od int) {
	for _, c := range calls {
		if c.spot {
			spot++
		} else {
			od++
		}
	}
	return
}

func TestLaunch_FirstSpotSucceeds(t *testing.T) {
	f := &fakeEC2{}
	l := NewLauncher(f, LauncherOptions{})
	inst, err := l.Launch(context.Background(), spec([]string{"c6i.xlarge", "c5.xlarge"}, []string{"sn-a"}))
	if err != nil || inst.ID != "i-abc" {
		t.Fatalf("got (%v, %v), want (i-abc, nil)", inst.ID, err)
	}
	spot, od := spotCalls(f.calls)
	if spot != 1 || od != 0 {
		t.Errorf("calls spot=%d od=%d, want 1/0", spot, od)
	}
}

func TestLaunch_CapacityThenNextSpotSucceeds(t *testing.T) {
	f := &fakeEC2{errs: []error{apiErr("InsufficientInstanceCapacity"), nil}}
	l := NewLauncher(f, LauncherOptions{})
	if _, err := l.Launch(context.Background(), spec([]string{"c6i.xlarge", "c5.xlarge"}, []string{"sn-a"})); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if _, od := spotCalls(f.calls); od != 0 {
		t.Errorf("on-demand calls = %d, want 0 (spot succeeded on 2nd candidate)", od)
	}
}

func TestLaunch_AllSpotFailFallsToOnDemand(t *testing.T) {
	f := &fakeEC2{errs: []error{apiErr("InsufficientInstanceCapacity"), apiErr("InsufficientInstanceCapacity"), nil}}
	l := NewLauncher(f, LauncherOptions{})
	if _, err := l.Launch(context.Background(), spec([]string{"c6i.xlarge", "c5.xlarge"}, []string{"sn-a"})); err != nil {
		t.Fatalf("err = %v, want nil (on-demand fallback succeeds)", err)
	}
	spot, od := spotCalls(f.calls)
	if spot != 2 || od != 1 {
		t.Errorf("calls spot=%d od=%d, want 2/1", spot, od)
	}
	if f.calls[len(f.calls)-1].instanceType != "c6i.xlarge" {
		t.Errorf("on-demand used %q, want primary c6i.xlarge", f.calls[len(f.calls)-1].instanceType)
	}
}

func TestLaunch_FatalAbortsImmediately(t *testing.T) {
	f := &fakeEC2{errs: []error{apiErr("UnauthorizedOperation")}}
	l := NewLauncher(f, LauncherOptions{})
	if _, err := l.Launch(context.Background(), spec([]string{"c6i.xlarge", "c5.xlarge"}, []string{"sn-a", "sn-b"})); err == nil {
		t.Fatal("want error, got nil")
	}
	if len(f.calls) != 1 {
		t.Errorf("calls = %d, want 1 (fatal aborts, no iteration, no on-demand)", len(f.calls))
	}
}

func TestLaunch_UnknownErrorStaysRetryable(t *testing.T) {
	f := &fakeEC2{errs: []error{apiErr("SomeNovelSpotError"), nil}}
	l := NewLauncher(f, LauncherOptions{})
	if _, err := l.Launch(context.Background(), spec([]string{"c5.xlarge"}, []string{"sn-a"})); err != nil {
		t.Fatalf("err = %v, want nil (unknown error retried → on-demand success)", err)
	}
	if _, od := spotCalls(f.calls); od != 1 {
		t.Errorf("on-demand calls = %d, want 1", od)
	}
}

func TestLaunch_NonAPIErrorStaysRetryable(t *testing.T) {
	f := &fakeEC2{errs: []error{errors.New("dial tcp: timeout"), nil}}
	l := NewLauncher(f, LauncherOptions{})
	if _, err := l.Launch(context.Background(), spec([]string{"c5.xlarge"}, []string{"sn-a"})); err != nil {
		t.Fatalf("err = %v, want nil (transport error → on-demand)", err)
	}
}

func TestLaunch_EmptySubnetsNoPanic(t *testing.T) {
	f := &fakeEC2{}
	l := NewLauncher(f, LauncherOptions{})
	if _, err := l.Launch(context.Background(), spec([]string{"c5.xlarge"}, nil)); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if f.calls[0].subnet != "" {
		t.Errorf("subnet = %q, want empty (EC2 picks default)", f.calls[0].subnet)
	}
}

func TestLaunch_NoInstanceTypesErrors(t *testing.T) {
	f := &fakeEC2{}
	l := NewLauncher(f, LauncherOptions{})
	if _, err := l.Launch(context.Background(), spec(nil, []string{"sn-a"})); err == nil {
		t.Fatal("want error for empty InstanceTypes, got nil")
	}
	if len(f.calls) != 0 {
		t.Errorf("calls = %d, want 0 (should not attempt any launch)", len(f.calls))
	}
}

type creditCapture struct {
	fakeEC2
	last *types.CreditSpecificationRequest
}

func (c *creditCapture) RunInstances(ctx context.Context, in *awsec2.RunInstancesInput, o ...func(*awsec2.Options)) (*awsec2.RunInstancesOutput, error) {
	c.last = in.CreditSpecification
	return c.fakeEC2.RunInstances(ctx, in, o...)
}

func TestRunInstance_BurstableGetsStandardCredits(t *testing.T) {
	c := &creditCapture{}
	l := NewLauncher(c, LauncherOptions{CPUCredits: "standard"})
	if _, err := l.Launch(context.Background(), spec([]string{"t3.medium"}, []string{"sn-a"})); err != nil {
		t.Fatal(err)
	}
	if c.last == nil || aws.ToString(c.last.CpuCredits) != "standard" {
		t.Errorf("CreditSpecification = %+v, want CpuCredits=standard", c.last)
	}
}

func TestRunInstance_NonBurstableNoCredits(t *testing.T) {
	c := &creditCapture{}
	l := NewLauncher(c, LauncherOptions{CPUCredits: "standard"})
	if _, err := l.Launch(context.Background(), spec([]string{"c5.xlarge"}, []string{"sn-a"})); err != nil {
		t.Fatal(err)
	}
	if c.last != nil {
		t.Errorf("CreditSpecification = %+v, want nil for non-burstable", c.last)
	}
}

func TestRotate(t *testing.T) {
	subnets := []string{"a", "b", "c"}

	// Deterministic: same seed → same order.
	if got1, got2 := rotate(subnets, "r1"), rotate(subnets, "r1"); !slices.Equal(got1, got2) {
		t.Errorf("rotate not deterministic: %v vs %v", got1, got2)
	}

	// Preserves length and is a permutation (all elements present).
	got := rotate(subnets, "r1")
	if len(got) != len(subnets) {
		t.Fatalf("len = %d, want %d", len(got), len(subnets))
	}
	sorted := append([]string(nil), got...)
	slices.Sort(sorted)
	if !slices.Equal(sorted, []string{"a", "b", "c"}) {
		t.Errorf("rotate dropped/added elements: %v", got)
	}

	// len<=1 passthrough (no panic, unchanged).
	if got := rotate([]string{"only"}, "x"); !slices.Equal(got, []string{"only"}) {
		t.Errorf("single-element rotate = %v, want [only]", got)
	}
	if got := rotate(nil, "x"); len(got) != 0 {
		t.Errorf("nil rotate = %v, want empty", got)
	}

	// Spread: at least two different RunnerIDs produce different start offsets
	// across a reasonable sample (probabilistic but near-certain for 3 subnets).
	seen := map[string]bool{}
	for _, s := range []string{"r1", "r2", "r3", "r4", "r5", "r6"} {
		seen[rotate(subnets, s)[0]] = true
	}
	if len(seen) < 2 {
		t.Errorf("rotate did not spread across subnets: only start %v seen", seen)
	}
}
