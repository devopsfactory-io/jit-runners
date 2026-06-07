variable "runner_version" {
  type        = string
  default     = "2.334.0"
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

variable "ami_groups" {
  type        = list(string)
  default     = ["all"]
  description = "Launch permission groups. Use [\"all\"] for public, [] for private."
}

variable "jit_runners_version" {
  type        = string
  default     = "dev"
  description = "jit-runners project version (e.g. v0.3.0). Defaults to 'dev' for local builds."
}

# --- GCP-specific variables (D9: googlecompute source) ---

variable "gcp_project" {
  type        = string
  default     = ""
  description = "GCP project ID for the GCE image build. Required for googlecompute source."
}

variable "gcp_zone" {
  type        = string
  default     = "us-central1-c"
  description = "GCP zone for the Packer build instance. Storage locations are configured separately via gcp_image_storage_locations. Changed from us-central1-a to us-central1-c due to recurring `does not have enough resources` capacity errors observed during the v1.0.0-rc.2 release (2026-05-04)."
}

variable "gcp_source_image_family" {
  type        = string
  default     = "ubuntu-2404-lts-amd64"
  description = "Source image family for the GCE build instance (Ubuntu 24.04 LTS amd64)."
}

variable "gcp_machine_type" {
  type        = string
  default     = "n2-standard-2"
  description = "Machine type for the Packer build instance on GCP."
}

variable "gcp_image_storage_locations" {
  type        = list(string)
  default     = ["us"]
  description = "Multi-region or region storage locations for the published GCE image. Use [\"us\"] for US multi-region, [\"us\", \"eu\", \"asia\"] for tri-multi-region distribution."
}
