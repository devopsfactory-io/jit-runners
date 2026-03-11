package ec2

// LaunchConfig determines the EC2 instance parameters for a runner.
type LaunchConfig struct {
	InstanceType       string
	AMI                string
	SubnetID           string
	SecurityGroupID    string
	IAMInstanceProfile string
	SpotMaxPrice       string // empty = on-demand price cap
	Labels             []string
	UserData           string // base64-encoded
	Tags               map[string]string
}
