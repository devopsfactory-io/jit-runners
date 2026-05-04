# ============================================================================
# Function URIs
# ============================================================================

output "webhook_url" {
  description = "URL to configure as the GitHub App webhook endpoint. Public-facing (ingress=ALLOW_ALL); HMAC-verified in the function code."
  value       = google_cloudfunctions2_function.webhook.service_config[0].uri
}

output "scaleup_url" {
  description = "Internal URL for the scaleup function (Eventarc target). Operators rarely call this directly."
  value       = google_cloudfunctions2_function.scaleup.service_config[0].uri
}

output "scaledown_url" {
  description = "Internal URL for the scaledown function (Cloud Scheduler target)."
  value       = google_cloudfunctions2_function.scaledown.service_config[0].uri
}

output "lifecycle_url" {
  description = "Internal URL for the lifecycle function (Eventarc target)."
  value       = google_cloudfunctions2_function.lifecycle.service_config[0].uri
}

output "rebalancer_url" {
  description = "Internal URL for the rebalancer function (Cloud Scheduler target)."
  value       = google_cloudfunctions2_function.rebalancer.service_config[0].uri
}

# ============================================================================
# Storage / Pub/Sub / Firestore
# ============================================================================

output "function_source_bucket" {
  description = "GCS bucket holding versioned function source zips. Operators do not write to this — Terraform fetches from GitHub Releases per D11."
  value       = google_storage_bucket.functions.name
}

output "jobs_topic" {
  description = "Pub/Sub topic for scaleup messages. Use with `gcloud pubsub topics publish` for manual injection during ops debugging."
  value       = google_pubsub_topic.jobs.id
}

output "lifecycle_topic" {
  description = "Pub/Sub topic for lifecycle messages (workflow_job in_progress/completed)."
  value       = google_pubsub_topic.lifecycle.id
}

output "jobs_dlq_inspector_subscription" {
  description = "Pull subscription for inspecting jobs DLQ messages via `gcloud pubsub subscriptions pull`."
  value       = google_pubsub_subscription.jobs_dlq_inspector.name
}

output "lifecycle_dlq_inspector_subscription" {
  description = "Pull subscription for inspecting lifecycle DLQ messages via `gcloud pubsub subscriptions pull`."
  value       = google_pubsub_subscription.lifecycle_dlq_inspector.name
}

output "runners_collection" {
  description = "Firestore collection name for runner state records. Use with `gcloud firestore documents` for ops debugging."
  value       = var.firestore_collection
}
