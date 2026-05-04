# ============================================================================
# Cloud Scheduler jobs
# ============================================================================

# Scheduler-invoker SA needs roles/run.invoker on each target function.

resource "google_cloud_run_v2_service_iam_member" "scheduler_invokes_scaledown" {
  project  = var.gcp_project
  location = var.gcp_region
  name     = google_cloudfunctions2_function.scaledown.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.scheduler_invoker.email}"
}

resource "google_cloud_run_v2_service_iam_member" "scheduler_invokes_rebalancer" {
  project  = var.gcp_project
  location = var.gcp_region
  name     = google_cloudfunctions2_function.rebalancer.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.scheduler_invoker.email}"
}

# scaledown: every 5 minutes (mirrors AWS EventBridge rate(5 minutes))

resource "google_cloud_scheduler_job" "scaledown" {
  name        = "${var.prefix}-scaledown"
  description = "Triggers the jit-runners scaledown function every 5 minutes."
  schedule    = "*/5 * * * *"
  time_zone   = "Etc/UTC"
  region      = var.gcp_region

  http_target {
    http_method = "POST"
    uri         = google_cloudfunctions2_function.scaledown.service_config[0].uri

    oidc_token {
      service_account_email = google_service_account.scheduler_invoker.email
      audience              = google_cloudfunctions2_function.scaledown.service_config[0].uri
    }
  }

  depends_on = [google_cloud_run_v2_service_iam_member.scheduler_invokes_scaledown]
}

# rebalancer: every minute (mirrors AWS EventBridge rate(1 minute))

resource "google_cloud_scheduler_job" "rebalancer" {
  name        = "${var.prefix}-rebalancer"
  description = "Triggers the jit-runners rebalancer function every minute."
  schedule    = "* * * * *"
  time_zone   = "Etc/UTC"
  region      = var.gcp_region

  http_target {
    http_method = "POST"
    uri         = google_cloudfunctions2_function.rebalancer.service_config[0].uri

    oidc_token {
      service_account_email = google_service_account.scheduler_invoker.email
      audience              = google_cloudfunctions2_function.rebalancer.service_config[0].uri
    }
  }

  depends_on = [google_cloud_run_v2_service_iam_member.scheduler_invokes_rebalancer]
}
