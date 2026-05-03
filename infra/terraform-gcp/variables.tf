# ----------------------------------------------------------------------------
# Identity
# ----------------------------------------------------------------------------

variable "gcp_project" {
  description = "GCP project ID where jit-runners deploys."
  type        = string
}

variable "gcp_region" {
  description = "Default GCP region for regional resources (Cloud Run functions, Cloud Scheduler, GCS bucket location)."
  type        = string
  default     = "us-central1"
}

variable "prefix" {
  description = "Resource name prefix. All resources are named <prefix>-<role> (e.g. jit-runners-webhook, jit-runners-jobs)."
  type        = string
  default     = "jit-runners"
}

# ----------------------------------------------------------------------------
# Release pinning (D11)
# ----------------------------------------------------------------------------

variable "release_tag" {
  description = "jit-runners GitHub release tag whose function zips this deploy uses (e.g. \"v1.0.0-rc.5\"). Bumping this and re-applying re-deploys all five Cloud Run functions."
  type        = string
}

# ----------------------------------------------------------------------------
# Firestore (D12 — feature flag for greenfield bootstrap)
# ----------------------------------------------------------------------------

variable "create_firestore_database" {
  description = "Whether to create the (default) Firestore Native database. Set true for fresh GCP projects with no Firestore yet; set false for projects that already have one (most common case)."
  type        = bool
  default     = false
}

variable "firestore_collection" {
  description = "Firestore collection name for jit-runners runner records."
  type        = string
  default     = "runners"
}

# ----------------------------------------------------------------------------
# Runner image (built by D-Packer; referenced here)
# ----------------------------------------------------------------------------

variable "runner_image" {
  description = "Full GCE image URI to use as the runner VM base image (e.g. projects/<maintainer-project>/global/images/jit-runner-v1-runner2.332.0-1700000000)."
  type        = string
}

variable "runner_network" {
  description = "VPC network name or full path for runner VMs."
  type        = string
  default     = "default"
}

variable "runner_subnet" {
  description = "VPC subnetwork full path for runner VMs (e.g. projects/<p>/regions/<r>/subnetworks/<s>)."
  type        = string
}

variable "runner_zone" {
  description = "GCE zone where runner VMs launch."
  type        = string
  default     = "us-central1-a"
}

# ----------------------------------------------------------------------------
# GitHub App credentials
# ----------------------------------------------------------------------------

variable "github_app_id" {
  description = "GitHub App ID (numeric), as a string."
  type        = string
}

variable "github_installation_id" {
  description = "GitHub App installation ID (numeric), as a string."
  type        = string
}

variable "webhook_secret_value" {
  description = "GitHub App webhook secret (HMAC) — written to Secret Manager."
  type        = string
  sensitive   = true
}

variable "github_app_private_key" {
  description = "GitHub App private key in PEM format — written to Secret Manager."
  type        = string
  sensitive   = true
}

# ----------------------------------------------------------------------------
# Cloud Run function scaling knobs
# ----------------------------------------------------------------------------

variable "function_memory" {
  description = "Memory allocation for Cloud Run functions (e.g. \"256M\")."
  type        = string
  default     = "256M"
}

variable "function_timeout_seconds" {
  description = "Timeout in seconds for Cloud Run function invocations."
  type        = number
  default     = 60
}

variable "max_instance_count" {
  description = "Maximum concurrent instances for scaling-flexible functions (webhook, scaleup, lifecycle). scaledown and rebalancer are always pinned to 1."
  type        = number
  default     = 10
}

# ----------------------------------------------------------------------------
# Operational thresholds (parity with AWS Terraform module)
# ----------------------------------------------------------------------------

variable "label_mappings" {
  description = "JSON-encoded array of label-to-machine-type mappings. Empty array (\"[]\") means use default machine type for all labels."
  type        = string
  default     = "[]"
}

variable "max_runner_age_minutes" {
  description = "Force-terminate runner VMs older than this. Mirrors AWS MaxRunnerAgeMinutes."
  type        = number
  default     = 360
}

variable "stale_threshold_minutes" {
  description = "Pending runners older than this are considered stuck and re-enqueued. Mirrors AWS StaleThresholdMinutes."
  type        = number
  default     = 10
}

variable "max_re_enqueue_attempts" {
  description = "Stuck pending runners are re-enqueued up to this many times before going terminal. Mirrors AWS MaxReEnqueueAttempts."
  type        = number
  default     = 3
}
