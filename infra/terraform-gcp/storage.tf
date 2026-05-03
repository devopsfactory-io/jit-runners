# ----------------------------------------------------------------------------
# Function source bucket
#
# Cloud Functions Gen 2 requires the build source in GCS or Cloud Source
# Repositories — there is no native "fetch from URL" option. We use GCS.
# Operators never touch this bucket directly: the data.http chain below
# downloads from GitHub Releases and uploads here automatically.
# ----------------------------------------------------------------------------

resource "google_storage_bucket" "functions" {
  name                        = "${var.prefix}-functions-${random_id.bucket_suffix.hex}"
  location                    = var.gcp_region
  force_destroy               = true
  uniform_bucket_level_access = true

  versioning { enabled = true }

  # Retain old function source zips for 90 days, then auto-clean.
  # Lets operators roll back to a previous release_tag for the retention window.
  lifecycle_rule {
    condition { age = 90 }
    action { type = "Delete" }
  }
}

# ----------------------------------------------------------------------------
# GitHub Release fetch chain (D11)
#
# Per-function 3-step chain:
#   1. data.http downloads the zip body, base64-encoded.
#   2. local_file writes the base64-decoded bytes to disk under .cache/.
#   3. google_storage_bucket_object uploads the file to the bucket.
#
# All three resources use for_each over local.function_names so the chain
# scales to all 5 functions with no duplication.
# ----------------------------------------------------------------------------

# 1. Fetch each zip from the GitHub Release as base64-encoded body.
data "http" "function_zips" {
  for_each = toset(local.function_names)
  url      = "https://github.com/devopsfactory-io/jit-runners/releases/download/${var.release_tag}/${each.key}.zip"
}

# 2. Land each base64-decoded body on local disk (Terraform-managed cache).
resource "local_file" "function_zips" {
  for_each       = toset(local.function_names)
  filename       = "${path.module}/.cache/${var.release_tag}/${each.key}.zip"
  content_base64 = data.http.function_zips[each.key].response_body_base64
}

# 3. Upload to GCS so Cloud Functions Gen 2 can build from it.
resource "google_storage_bucket_object" "function_zip" {
  for_each = toset(local.function_names)
  name     = "${var.release_tag}/${each.key}.zip"
  bucket   = google_storage_bucket.functions.name
  source   = local_file.function_zips[each.key].filename
}
