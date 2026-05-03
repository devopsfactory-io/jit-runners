# ============================================================================
# Cloud Run functions Gen 2 (D10)
#
# Five functions: webhook, scaleup, scaledown, lifecycle, rebalancer.
# All consume their source zip from the GCS bucket populated by storage.tf
# (which itself fetches from GitHub Releases per D11).
#
# Per-function knobs differ:
#   - webhook: ingress=ALLOW_ALL (public-facing; HMAC verify is the trust boundary)
#   - scaleup, lifecycle: ingress=ALLOW_INTERNAL_ONLY (Eventarc-only)
#   - scaledown, rebalancer: ingress=ALLOW_INTERNAL_ONLY (Cloud Scheduler-only)
#   - max_instance_count: 1 for scaledown + rebalancer (serial execution),
#     var.max_instance_count for the others
# ============================================================================

# ----------------------------------------------------------------------------
# webhook — public HTTPS endpoint, publishes workflow_job events to Pub/Sub
# ----------------------------------------------------------------------------

resource "google_cloudfunctions2_function" "webhook" {
  name     = "${var.prefix}-webhook"
  location = var.gcp_region

  build_config {
    runtime     = "go122"
    entry_point = "Handler"

    source {
      storage_source {
        bucket = google_storage_bucket.functions.name
        object = google_storage_bucket_object.function_zip["webhook"].name
      }
    }
  }

  service_config {
    available_memory      = var.function_memory
    timeout_seconds       = var.function_timeout_seconds
    max_instance_count    = var.max_instance_count
    ingress_settings      = "ALLOW_ALL"
    service_account_email = google_service_account.webhook.email

    environment_variables = {
      CLOUD_PROVIDER         = "gcp"
      GCP_PROJECT            = var.gcp_project
      GCP_REGION             = var.gcp_region
      PUBSUB_JOBS_TOPIC      = google_pubsub_topic.jobs.id
      PUBSUB_LIFECYCLE_TOPIC = google_pubsub_topic.lifecycle.id
      LOG_LEVEL              = "info"
    }

    secret_environment_variables {
      key        = "WEBHOOK_SECRET"
      project_id = var.gcp_project
      secret     = google_secret_manager_secret.webhook_secret.secret_id
      version    = "latest"
    }

    secret_environment_variables {
      key        = "GITHUB_APP_KEY"
      project_id = var.gcp_project
      secret     = google_secret_manager_secret.github_app_key.secret_id
      version    = "latest"
    }
  }

  depends_on = [google_project_service.apis]
}

# ----------------------------------------------------------------------------
# scaleup — Eventarc-triggered; consumes jobs topic; launches GCE VMs
# ----------------------------------------------------------------------------

resource "google_cloudfunctions2_function" "scaleup" {
  name     = "${var.prefix}-scaleup"
  location = var.gcp_region

  build_config {
    runtime     = "go122"
    entry_point = "Handler"

    source {
      storage_source {
        bucket = google_storage_bucket.functions.name
        object = google_storage_bucket_object.function_zip["scaleup"].name
      }
    }
  }

  service_config {
    available_memory      = var.function_memory
    timeout_seconds       = var.function_timeout_seconds
    max_instance_count    = var.max_instance_count
    ingress_settings      = "ALLOW_INTERNAL_ONLY"
    service_account_email = google_service_account.scaleup.email

    environment_variables = {
      CLOUD_PROVIDER          = "gcp"
      GCP_PROJECT             = var.gcp_project
      GCP_REGION              = var.gcp_region
      GITHUB_APP_ID           = var.github_app_id
      GITHUB_INSTALLATION_ID  = var.github_installation_id
      PUBSUB_JOBS_TOPIC       = google_pubsub_topic.jobs.id
      FIRESTORE_DATABASE      = "(default)"
      FIRESTORE_COLLECTION    = var.firestore_collection
      RUNNER_NETWORK          = var.runner_network
      RUNNER_SUBNET           = var.runner_subnet
      RUNNER_IMAGE            = var.runner_image
      RUNNER_SA_EMAIL         = google_service_account.runner.email
      RUNNER_ZONE             = var.runner_zone
      LABEL_MAPPINGS          = var.label_mappings
      MAX_RE_ENQUEUE_ATTEMPTS = tostring(var.max_re_enqueue_attempts)
      LOG_LEVEL               = "info"
    }

    secret_environment_variables {
      key        = "GITHUB_APP_KEY"
      project_id = var.gcp_project
      secret     = google_secret_manager_secret.github_app_key.secret_id
      version    = "latest"
    }
  }

  depends_on = [google_project_service.apis]
}

# ----------------------------------------------------------------------------
# scaledown — Cloud-Scheduler-triggered every 5 min; cleanup + re-enqueue
# ----------------------------------------------------------------------------

resource "google_cloudfunctions2_function" "scaledown" {
  name     = "${var.prefix}-scaledown"
  location = var.gcp_region

  build_config {
    runtime     = "go122"
    entry_point = "Handler"

    source {
      storage_source {
        bucket = google_storage_bucket.functions.name
        object = google_storage_bucket_object.function_zip["scaledown"].name
      }
    }
  }

  service_config {
    available_memory      = var.function_memory
    timeout_seconds       = var.function_timeout_seconds
    max_instance_count    = 1
    ingress_settings      = "ALLOW_INTERNAL_ONLY"
    service_account_email = google_service_account.scaledown.email

    environment_variables = {
      CLOUD_PROVIDER          = "gcp"
      GCP_PROJECT             = var.gcp_project
      GCP_REGION              = var.gcp_region
      GITHUB_APP_ID           = var.github_app_id
      GITHUB_INSTALLATION_ID  = var.github_installation_id
      PUBSUB_JOBS_TOPIC       = google_pubsub_topic.jobs.id
      FIRESTORE_DATABASE      = "(default)"
      FIRESTORE_COLLECTION    = var.firestore_collection
      RUNNER_ZONE             = var.runner_zone
      MAX_RUNNER_AGE_MINUTES  = tostring(var.max_runner_age_minutes)
      STALE_THRESHOLD_MINUTES = tostring(var.stale_threshold_minutes)
      MAX_RE_ENQUEUE_ATTEMPTS = tostring(var.max_re_enqueue_attempts)
      LOG_LEVEL               = "info"
    }

    secret_environment_variables {
      key        = "GITHUB_APP_KEY"
      project_id = var.gcp_project
      secret     = google_secret_manager_secret.github_app_key.secret_id
      version    = "latest"
    }
  }

  depends_on = [google_project_service.apis]
}

# ----------------------------------------------------------------------------
# lifecycle — Eventarc-triggered; processes workflow_job in_progress/completed
# ----------------------------------------------------------------------------

resource "google_cloudfunctions2_function" "lifecycle" {
  name     = "${var.prefix}-lifecycle"
  location = var.gcp_region

  build_config {
    runtime     = "go122"
    entry_point = "Handler"

    source {
      storage_source {
        bucket = google_storage_bucket.functions.name
        object = google_storage_bucket_object.function_zip["lifecycle"].name
      }
    }
  }

  service_config {
    available_memory      = var.function_memory
    timeout_seconds       = var.function_timeout_seconds
    max_instance_count    = var.max_instance_count
    ingress_settings      = "ALLOW_INTERNAL_ONLY"
    service_account_email = google_service_account.lifecycle.email

    environment_variables = {
      CLOUD_PROVIDER         = "gcp"
      GCP_PROJECT            = var.gcp_project
      GCP_REGION             = var.gcp_region
      GITHUB_APP_ID          = var.github_app_id
      GITHUB_INSTALLATION_ID = var.github_installation_id
      FIRESTORE_DATABASE     = "(default)"
      FIRESTORE_COLLECTION   = var.firestore_collection
      LOG_LEVEL              = "info"
    }

    secret_environment_variables {
      key        = "GITHUB_APP_KEY"
      project_id = var.gcp_project
      secret     = google_secret_manager_secret.github_app_key.secret_id
      version    = "latest"
    }
  }

  depends_on = [google_project_service.apis]
}

# ----------------------------------------------------------------------------
# rebalancer — Cloud-Scheduler-triggered every 1 min; drift recovery
# ----------------------------------------------------------------------------

resource "google_cloudfunctions2_function" "rebalancer" {
  name     = "${var.prefix}-rebalancer"
  location = var.gcp_region

  build_config {
    runtime     = "go122"
    entry_point = "Handler"

    source {
      storage_source {
        bucket = google_storage_bucket.functions.name
        object = google_storage_bucket_object.function_zip["rebalancer"].name
      }
    }
  }

  service_config {
    available_memory      = var.function_memory
    timeout_seconds       = var.function_timeout_seconds
    max_instance_count    = 1
    ingress_settings      = "ALLOW_INTERNAL_ONLY"
    service_account_email = google_service_account.rebalancer.email

    environment_variables = {
      CLOUD_PROVIDER         = "gcp"
      GCP_PROJECT            = var.gcp_project
      GCP_REGION             = var.gcp_region
      GITHUB_APP_ID          = var.github_app_id
      GITHUB_INSTALLATION_ID = var.github_installation_id
      PUBSUB_JOBS_TOPIC      = google_pubsub_topic.jobs.id
      FIRESTORE_DATABASE     = "(default)"
      FIRESTORE_COLLECTION   = var.firestore_collection
      LOG_LEVEL              = "info"
    }

    secret_environment_variables {
      key        = "GITHUB_APP_KEY"
      project_id = var.gcp_project
      secret     = google_secret_manager_secret.github_app_key.secret_id
      version    = "latest"
    }
  }

  depends_on = [google_project_service.apis]
}
