variable "aws_region" {
  description = "AWS region for all resources"
  type        = string
  default     = "us-east-1"
}

variable "project_name" {
  description = "Project name used for resource naming"
  type        = string
  default     = "jit-runners"
}

# --- GitHub App ---

variable "github_app_id" {
  description = "GitHub App ID"
  type        = string
}

variable "github_installation_id" {
  description = "GitHub App installation ID (numeric) used by lifecycle and scaledown for installation-token minting"
  type        = string
}

variable "webhook_secret_arn" {
  description = "ARN of the Secrets Manager secret containing the GitHub webhook secret"
  type        = string
}

variable "private_key_arn" {
  description = "ARN of the Secrets Manager secret containing the GitHub App private key"
  type        = string
}

# --- Networking ---

variable "vpc_id" {
  description = "VPC ID where runner EC2 instances will launch"
  type        = string
}

variable "subnet_ids" {
  description = "Subnet IDs for runner EC2 instances (private subnets recommended)"
  type        = list(string)
}

# --- EC2 ---

variable "default_ami" {
  description = "Default AMI ID for runner instances (Amazon Linux 2023 recommended)"
  type        = string
}

variable "default_instance_type" {
  description = "Default EC2 instance type for runners when no LABEL_MAPPINGS entry matches the requested labels."
  type        = string
  default     = "t3.large"
}

variable "label_mappings" {
  description = "JSON-encoded label-to-instance-type mappings"
  type        = string
  default     = "[{\"label\":\"nano\",\"instance_type\":\"t3a.nano\",\"instance_types\":[\"t3a.nano\",\"t3.nano\",\"t3a.micro\",\"t3.micro\"]},{\"label\":\"micro\",\"instance_type\":\"t3a.micro\",\"instance_types\":[\"t3a.micro\",\"t3.micro\",\"t3a.small\",\"t3.small\"]},{\"label\":\"small\",\"instance_type\":\"t3a.small\",\"instance_types\":[\"t3a.small\",\"t3.small\",\"t3a.medium\",\"t3.medium\"]},{\"label\":\"medium\",\"instance_type\":\"t3.medium\",\"instance_types\":[\"t3.medium\",\"t3a.medium\",\"m6i.large\",\"m5.large\"]},{\"label\":\"large\",\"instance_type\":\"c6i.xlarge\",\"instance_types\":[\"c6i.xlarge\",\"c5.xlarge\",\"c5a.xlarge\",\"m6i.xlarge\"]},{\"label\":\"release\",\"instance_type\":\"m5.xlarge\",\"instance_types\":[\"m5.xlarge\",\"m5a.xlarge\",\"m6i.xlarge\",\"m6a.xlarge\"]}]"
}

variable "runner_version" {
  description = "GitHub Actions runner version the ephemeral runners register with. Keep current — GitHub deprecates old versions and refuses to dispatch jobs to them. If it differs from the pre-baked AMI's version, the runner is downloaded at launch (adds cold-start latency); rebuild the AMI to match."
  type        = string
  default     = "2.334.0"
}

# --- Lambda ---

variable "webhook_lambda_s3_bucket" {
  description = "S3 bucket containing Lambda deployment packages"
  type        = string
}

variable "webhook_lambda_s3_key" {
  description = "S3 key for the webhook Lambda zip"
  type        = string
}

variable "scaleup_lambda_s3_key" {
  description = "S3 key for the scale-up Lambda zip"
  type        = string
}

variable "scaledown_lambda_s3_key" {
  description = "S3 key for the scale-down Lambda zip"
  type        = string
}

variable "lifecycle_lambda_s3_key" {
  description = "S3 key for the lifecycle Lambda zip"
  type        = string
}

variable "rebalancer_lambda_s3_key" {
  type        = string
  description = "S3 key for the rebalancer Lambda zip (e.g. v1.0.0-rc.4/rebalancer.zip)."
}

# --- Scale-down ---

variable "stale_threshold_minutes" {
  description = "Minutes before a pending runner is considered stale"
  type        = number
  default     = 10
}

variable "max_runner_age_minutes" {
  description = "Maximum age in minutes before a running instance is force-terminated"
  type        = number
  default     = 360
}

variable "max_re_enqueue_attempts" {
  description = "Max re-enqueue attempts before a stuck pending job goes terminal"
  type        = number
  default     = 3
}
