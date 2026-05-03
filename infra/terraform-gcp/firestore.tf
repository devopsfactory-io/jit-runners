# ============================================================================
# Firestore database (D12 — feature flag for greenfield bootstrap)
#
# When create_firestore_database = true:
#   - Creates the (default) Firestore Native database in var.gcp_region.
#   - delete_protection_state = DISABLED + deletion_policy = DELETE so
#     `terraform destroy` works on the rest of the module without orphaning.
#
# When create_firestore_database = false (default):
#   - This resource is skipped entirely; the module assumes the project
#     already has a (default) Firestore database. The TTL field policy
#     below references "(default)" by hard-coded name and works either way.
# ============================================================================

resource "google_firestore_database" "default" {
  count = var.create_firestore_database ? 1 : 0

  project                 = var.gcp_project
  name                    = "(default)"
  location_id             = var.gcp_region
  type                    = "FIRESTORE_NATIVE"
  delete_protection_state = "DELETE_PROTECTION_DISABLED"
  deletion_policy         = "DELETE"

  depends_on = [google_project_service.apis]
}

# ============================================================================
# TTL field policy
#
# Auto-deletes runner records once their `ttl` field's timestamp passes.
# Set by scaleup at runner creation time; mirrors AWS DynamoDB TTL.
# ============================================================================

resource "google_firestore_field" "runners_ttl" {
  project    = var.gcp_project
  database   = "(default)"
  collection = var.firestore_collection
  field      = "ttl"

  ttl_config {}

  depends_on = [google_firestore_database.default]
}
