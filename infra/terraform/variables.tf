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
  description = "Default EC2 instance type for runners"
  type        = string
  default     = "t3.medium"
}

variable "label_mappings" {
  description = "JSON-encoded label-to-instance-type mappings"
  type        = string
  default     = "[]"
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
