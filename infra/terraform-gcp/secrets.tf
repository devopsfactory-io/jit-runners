# ============================================================================
# Secret Manager secrets
# ============================================================================

resource "google_secret_manager_secret" "webhook_secret" {
  secret_id = "${var.prefix}-webhook-secret"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "webhook_secret" {
  secret      = google_secret_manager_secret.webhook_secret.id
  secret_data = var.webhook_secret_value
}

resource "google_secret_manager_secret" "github_app_key" {
  secret_id = "${var.prefix}-github-app-key"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "github_app_key" {
  secret      = google_secret_manager_secret.github_app_key.id
  secret_data = var.github_app_private_key
}

# ============================================================================
# Secret access bindings
#
# Each function SA gets secretAccessor on the secrets it actually consumes
# at runtime — least privilege per-secret.
# ============================================================================

# Webhook secret: only webhook function reads it (HMAC verification).

resource "google_secret_manager_secret_iam_member" "webhook_secret_webhook" {
  secret_id = google_secret_manager_secret.webhook_secret.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.webhook.email}"
}

# GitHub App private key: webhook (for installation token minting if it
# ever needs to call GitHub), scaleup (generate-jitconfig), scaledown
# (DeregisterRunner), lifecycle (DeregisterRunner), rebalancer
# (ListQueuedWorkflowJobs).

resource "google_secret_manager_secret_iam_member" "github_app_key_readers" {
  for_each = toset([
    google_service_account.webhook.email,
    google_service_account.scaleup.email,
    google_service_account.scaledown.email,
    google_service_account.lifecycle.email,
    google_service_account.rebalancer.email,
  ])

  secret_id = google_secret_manager_secret.github_app_key.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${each.value}"
}
