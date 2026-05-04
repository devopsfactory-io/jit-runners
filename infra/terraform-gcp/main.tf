# ----------------------------------------------------------------------------
# Provider config + project lookup
# ----------------------------------------------------------------------------

provider "google" {
  project = var.gcp_project
  region  = var.gcp_region

  default_labels = {
    managed-by = "jit-runners"
    project    = "jit-runners"
  }
}

data "google_project" "current" {
  project_id = var.gcp_project
}

# ----------------------------------------------------------------------------
# Locals
# ----------------------------------------------------------------------------

locals {
  function_names = ["webhook", "scaleup", "scaledown", "lifecycle", "rebalancer"]
}

# ----------------------------------------------------------------------------
# Random suffix for the function-source bucket (GCS bucket names are global)
# ----------------------------------------------------------------------------

resource "random_id" "bucket_suffix" {
  byte_length = 4
}

# ----------------------------------------------------------------------------
# Project API enablement (12 APIs)
#
# First-time apply must enable these before any resources can be created.
# Using google_project_service makes the dependency explicit so first-time
# applies don't fail on disabled APIs. Operators bringing existing projects
# usually have most of these on already; google_project_service is idempotent.
# ----------------------------------------------------------------------------

resource "google_project_service" "apis" {
  for_each = toset([
    "cloudfunctions.googleapis.com",
    "cloudbuild.googleapis.com",       # cloudfunctions Gen 2 builds via Cloud Build
    "artifactregistry.googleapis.com", # cloudfunctions Gen 2 stores built images in Artifact Registry
    "run.googleapis.com",               # cloudfunctions Gen 2 runs on Cloud Run
    "eventarc.googleapis.com",
    "pubsub.googleapis.com",
    "firestore.googleapis.com",
    "secretmanager.googleapis.com",
    "compute.googleapis.com",
    "cloudscheduler.googleapis.com",
    "storage.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com", # required for SA token minting
  ])

  project = var.gcp_project
  service = each.value

  # Don't disable APIs on `terraform destroy` — other workloads in the project
  # may rely on them. This is the conservative default for a multi-tenant
  # project. If the project is single-purpose, operators can flip this in a
  # post-merge customization fork.
  disable_on_destroy = false
}
