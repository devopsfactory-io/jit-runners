# ============================================================================
# Topics
# ============================================================================

resource "google_pubsub_topic" "jobs" {
  name = "${var.prefix}-jobs"
}

resource "google_pubsub_topic" "jobs_dlq" {
  name = "${var.prefix}-jobs-dlq"
}

resource "google_pubsub_topic" "lifecycle" {
  name = "${var.prefix}-lifecycle"
}

resource "google_pubsub_topic" "lifecycle_dlq" {
  name = "${var.prefix}-lifecycle-dlq"
}

# ============================================================================
# Active subscriptions with explicit DLQ + retry policy (D13)
#
# Eventarc triggers reference the underlying topics. These subscriptions
# carry the explicit DLQ + retry policy that mirrors AWS SQS RedrivePolicy.
# At first apply, verify via `gcloud eventarc triggers describe` that the
# active delivery actually uses these subs. If Eventarc creates its own
# managed subs and ignores ours, file a follow-up issue per D13 — operator
# workaround is to use the inspector subs on the topic for failed-message
# inspection.
# ============================================================================

resource "google_pubsub_subscription" "jobs_scaleup" {
  name                       = "${var.prefix}-jobs-scaleup"
  topic                      = google_pubsub_topic.jobs.name
  ack_deadline_seconds       = 60
  message_retention_duration = "86400s" # 24h

  expiration_policy {
    ttl = "" # never expires
  }

  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.jobs_dlq.id
    max_delivery_attempts = 3 # mirrors AWS RedrivePolicy.maxReceiveCount
  }

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }
}

resource "google_pubsub_subscription" "lifecycle_lifecycle" {
  name                       = "${var.prefix}-lifecycle-lifecycle"
  topic                      = google_pubsub_topic.lifecycle.name
  ack_deadline_seconds       = 90
  message_retention_duration = "604800s" # 7 days; mirrors AWS lifecycle queue retention

  expiration_policy {
    ttl = "" # never expires
  }

  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.lifecycle_dlq.id
    max_delivery_attempts = 5 # mirrors AWS RedrivePolicy.maxReceiveCount
  }

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }
}

# ============================================================================
# DLQ inspector subscriptions
#
# Passive subs (no push endpoint) so operators can `gcloud pubsub subscriptions
# pull` to inspect dead-lettered messages during ops debugging. 14-day retention
# mirrors AWS DLQ retention.
# ============================================================================

resource "google_pubsub_subscription" "jobs_dlq_inspector" {
  name                       = "${var.prefix}-jobs-dlq-inspector"
  topic                      = google_pubsub_topic.jobs_dlq.name
  message_retention_duration = "1209600s" # 14 days

  expiration_policy {
    ttl = ""
  }
}

resource "google_pubsub_subscription" "lifecycle_dlq_inspector" {
  name                       = "${var.prefix}-lifecycle-dlq-inspector"
  topic                      = google_pubsub_topic.lifecycle_dlq.name
  message_retention_duration = "1209600s" # 14 days

  expiration_policy {
    ttl = ""
  }
}

# ============================================================================
# Topic-level publisher grants
#
# webhook publishes to BOTH topics; rebalancer + scaledown publish to jobs
# only.
# ============================================================================

resource "google_pubsub_topic_iam_member" "webhook_jobs_publisher" {
  topic  = google_pubsub_topic.jobs.id
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:${google_service_account.webhook.email}"
}

resource "google_pubsub_topic_iam_member" "webhook_lifecycle_publisher" {
  topic  = google_pubsub_topic.lifecycle.id
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:${google_service_account.webhook.email}"
}

resource "google_pubsub_topic_iam_member" "rebalancer_jobs_publisher" {
  topic  = google_pubsub_topic.jobs.id
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:${google_service_account.rebalancer.email}"
}

resource "google_pubsub_topic_iam_member" "scaledown_jobs_publisher" {
  topic  = google_pubsub_topic.jobs.id
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:${google_service_account.scaledown.email}"
}
