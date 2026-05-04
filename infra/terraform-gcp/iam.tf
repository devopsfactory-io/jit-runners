# ============================================================================
# Service accounts
# ============================================================================

# 5 function SAs — one per Cloud Run function

resource "google_service_account" "webhook" {
  account_id   = "${var.prefix}-webhook-sa"
  display_name = "${var.prefix} webhook function"
  description  = "Identity for the jit-runners webhook function — publishes to jobs + lifecycle Pub/Sub topics."
}

resource "google_service_account" "scaleup" {
  account_id   = "${var.prefix}-scaleup-sa"
  display_name = "${var.prefix} scaleup function"
  description  = "Identity for the jit-runners scaleup function — Firestore RW, GCE instance launches, GitHub App key access."
}

resource "google_service_account" "scaledown" {
  account_id   = "${var.prefix}-scaledown-sa"
  display_name = "${var.prefix} scaledown function"
  description  = "Identity for the jit-runners scaledown function — Firestore RW, GCE instance termination, jobs topic re-enqueue."
}

resource "google_service_account" "lifecycle" {
  account_id   = "${var.prefix}-lifecycle-sa"
  display_name = "${var.prefix} lifecycle function"
  description  = "Identity for the jit-runners lifecycle function — Firestore RW, GitHub App key access for DeregisterRunner."
}

resource "google_service_account" "rebalancer" {
  account_id   = "${var.prefix}-rebalancer-sa"
  display_name = "${var.prefix} rebalancer function"
  description  = "Identity for the jit-runners rebalancer function — Firestore reads (ListActiveRepos), jobs topic publishes."
}

# Runner VM SA — bound to ephemeral GCE instances jit-runners launches

resource "google_service_account" "runner" {
  account_id   = "${var.prefix}-runner-sa"
  display_name = "${var.prefix} runner VM identity"
  description  = "Identity attached to ephemeral runner GCE VMs. logging.logWriter + monitoring.metricWriter + tightly-scoped self-terminate."
}

# Invoker SAs — Eventarc and Cloud Scheduler use these to invoke functions

resource "google_service_account" "eventarc_invoker" {
  account_id   = "${var.prefix}-eventarc-invoker-sa"
  display_name = "${var.prefix} Eventarc invoker"
  description  = "Eventarc OIDC identity for invoking scaleup + lifecycle Cloud Run functions."
}

resource "google_service_account" "scheduler_invoker" {
  account_id   = "${var.prefix}-scheduler-invoker-sa"
  display_name = "${var.prefix} Cloud Scheduler invoker"
  description  = "Cloud Scheduler OIDC identity for invoking scaledown + rebalancer Cloud Run functions."
}

# ============================================================================
# Project-level role bindings (scaleup + scaledown need broad GCE permissions)
# ============================================================================

# scaleup: launch GCE instances + impersonate runner SA on the launched VMs
# Per spec D15, project-level for v1; scope-down deferred.

resource "google_project_iam_member" "scaleup_compute_admin" {
  project = var.gcp_project
  role    = "roles/compute.instanceAdmin.v1"
  member  = "serviceAccount:${google_service_account.scaleup.email}"
}

resource "google_service_account_iam_member" "scaleup_uses_runner_sa" {
  service_account_id = google_service_account.runner.id
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.scaleup.email}"
}

# scaledown: terminate GCE instances. Same project-level scope as scaleup
# (the same broad role is needed; scope-down via custom role is a follow-up
# per spec D15).

resource "google_project_iam_member" "scaledown_compute_admin" {
  project = var.gcp_project
  role    = "roles/compute.instanceAdmin.v1"
  member  = "serviceAccount:${google_service_account.scaledown.email}"
}

# Function SAs: Datastore (Firestore) read/write
# scaleup, scaledown, lifecycle, rebalancer all read+write Firestore.
# webhook does NOT read Firestore (it only publishes to Pub/Sub).

resource "google_project_iam_member" "datastore_user" {
  for_each = toset([
    google_service_account.scaleup.email,
    google_service_account.scaledown.email,
    google_service_account.lifecycle.email,
    google_service_account.rebalancer.email,
  ])

  project = var.gcp_project
  role    = "roles/datastore.user"
  member  = "serviceAccount:${each.value}"
}

# Runner VM SA: log + metric writer

resource "google_project_iam_member" "runner_log_writer" {
  project = var.gcp_project
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.runner.email}"
}

resource "google_project_iam_member" "runner_metric_writer" {
  project = var.gcp_project
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.runner.email}"
}

# ============================================================================
# Runner self-terminate (blast-radius containment, NOT scope-down per D15)
#
# Lets the runner SA delete only instances it can prove are jit-runners
# instances (label managed-by=jit-runners). Even if the runner SA is
# compromised, the blast radius is "delete other jit-runner VMs in this
# project" — bounded.
# ============================================================================

resource "google_project_iam_custom_role" "runner_self_terminate" {
  role_id     = "${replace(var.prefix, "-", "_")}_runner_self_terminate"
  title       = "${var.prefix} runner self-terminate"
  description = "Permits compute.instances.delete on instances named jit-runner-* only."
  permissions = ["compute.instances.delete"]
}

resource "google_project_iam_member" "runner_self_terminate" {
  project = var.gcp_project
  role    = google_project_iam_custom_role.runner_self_terminate.id
  member  = "serviceAccount:${google_service_account.runner.email}"

  condition {
    title       = "self-terminate own instance only"
    description = "Restricts runner SA to deleting only instances named jit-runner-* (the launcher's deterministic name prefix). resource.labels is not supported in GCP IAM CEL — see spec D15 follow-up."
    expression  = <<-EOT
      resource.type == "compute.googleapis.com/Instance" &&
      resource.name.contains("/instances/jit-runner-")
    EOT
  }
}

# ============================================================================
# Pub/Sub service-agent grants
#
# Pub/Sub-managed dead-letter delivery uses the project's gcp-sa-pubsub
# service agent. Without these grants, DLQ delivery silently drops failed
# messages.
# ============================================================================

resource "google_pubsub_topic_iam_member" "jobs_dlq_publisher" {
  topic  = google_pubsub_topic.jobs_dlq.id
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:service-${data.google_project.current.number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}

resource "google_pubsub_topic_iam_member" "lifecycle_dlq_publisher" {
  topic  = google_pubsub_topic.lifecycle_dlq.id
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:service-${data.google_project.current.number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}
