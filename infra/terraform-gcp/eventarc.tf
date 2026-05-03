# ============================================================================
# Eventarc Pub/Sub triggers (D3)
#
# Each trigger:
#   - Matches on Pub/Sub messagePublished events.
#   - References the underlying topic via transport.pubsub.topic.
#   - Targets a Cloud Run function via destination.cloud_run_service.
#   - Uses the eventarc-invoker SA for OIDC auth into the function.
#
# Per D13, whether the trigger binds to the explicit subscription declared
# in pubsub.tf or auto-creates its own is a runtime concern. Verification
# step: `gcloud eventarc triggers describe <trigger>` after first apply.
# ============================================================================

# Eventarc-invoker SA needs roles/run.invoker on each target function.
# We declare those bindings here so the trigger creation succeeds (Eventarc
# verifies invoker permission at trigger-create time).

resource "google_cloud_run_v2_service_iam_member" "eventarc_invokes_scaleup" {
  project  = var.gcp_project
  location = var.gcp_region
  name     = google_cloudfunctions2_function.scaleup.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.eventarc_invoker.email}"
}

resource "google_cloud_run_v2_service_iam_member" "eventarc_invokes_lifecycle" {
  project  = var.gcp_project
  location = var.gcp_region
  name     = google_cloudfunctions2_function.lifecycle.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.eventarc_invoker.email}"
}

# scaleup trigger: jobs topic → scaleup function

resource "google_eventarc_trigger" "scaleup" {
  name     = "${var.prefix}-scaleup"
  location = var.gcp_region

  matching_criteria {
    attribute = "type"
    value     = "google.cloud.pubsub.topic.v1.messagePublished"
  }

  transport {
    pubsub {
      topic = google_pubsub_topic.jobs.id
    }
  }

  destination {
    cloud_run_service {
      service = google_cloudfunctions2_function.scaleup.name
      region  = var.gcp_region
    }
  }

  service_account = google_service_account.eventarc_invoker.email

  depends_on = [google_cloud_run_v2_service_iam_member.eventarc_invokes_scaleup]
}

# lifecycle trigger: lifecycle topic → lifecycle function

resource "google_eventarc_trigger" "lifecycle" {
  name     = "${var.prefix}-lifecycle"
  location = var.gcp_region

  matching_criteria {
    attribute = "type"
    value     = "google.cloud.pubsub.topic.v1.messagePublished"
  }

  transport {
    pubsub {
      topic = google_pubsub_topic.lifecycle.id
    }
  }

  destination {
    cloud_run_service {
      service = google_cloudfunctions2_function.lifecycle.name
      region  = var.gcp_region
    }
  }

  service_account = google_service_account.eventarc_invoker.email

  depends_on = [google_cloud_run_v2_service_iam_member.eventarc_invokes_lifecycle]
}
