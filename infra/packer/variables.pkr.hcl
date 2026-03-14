variable "runner_version" {
  type        = string
  default     = "2.332.0"
  description = "GitHub Actions runner version to pre-install."
}

variable "aws_region" {
  type        = string
  default     = "us-east-2"
  description = "AWS region to build the AMI in."
}

variable "ami_regions" {
  type        = list(string)
  default     = []
  description = "Additional regions to copy the AMI to. Each copy is also made public."
}

# All supported distribution regions (US, Europe, South America)
# Use: -var 'ami_regions=var.ami_distribution_regions' or pass explicitly
variable "ami_distribution_regions" {
  type = list(string)
  default = [
    "us-east-1",
    "us-west-1",
    "us-west-2",
    "eu-west-1",
    "eu-west-2",
    "eu-west-3",
    "eu-central-1",
    "eu-north-1",
    "sa-east-1",
  ]
  description = "Pre-defined list of distribution regions (US, Europe, South America)."
}

variable "instance_type" {
  type        = string
  default     = "t3.medium"
  description = "Instance type for the Packer build."
}

variable "extra_script" {
  type        = string
  default     = ""
  description = "Optional path to a shell script for additional packages/setup."
}

variable "ami_name_prefix" {
  type        = string
  default     = "jit-runner"
  description = "Prefix for the AMI name."
}

variable "subnet_id" {
  type        = string
  default     = ""
  description = "Subnet ID for the build instance (optional, uses default VPC if empty)."
}

variable "go_version" {
  type        = string
  default     = "1.23.6"
  description = "Go version to pre-install in the AMI."
}

variable "node_major_version" {
  type        = string
  default     = "22"
  description = "Node.js major version (LTS) to pre-install in the AMI."
}

variable "volume_size" {
  type        = number
  default     = 30
  description = "Root EBS volume size in GB for the AMI."
}
