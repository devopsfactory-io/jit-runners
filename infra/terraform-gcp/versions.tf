terraform {
  required_version = ">= 1.6.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.30.0"
    }

    # http >= 3.4 required for response_body_base64 (D11 — binary download).
    http = {
      source  = "hashicorp/http"
      version = ">= 3.4.0"
    }

    # local >= 2.5 required for content_base64 (D11 — binary writeback).
    local = {
      source  = "hashicorp/local"
      version = ">= 2.5.0"
    }

    null = {
      source  = "hashicorp/null"
      version = ">= 3.2.0"
    }

    random = {
      source  = "hashicorp/random"
      version = ">= 3.6.0"
    }
  }

  # Configure your backend here.
  # backend "gcs" {
  #   bucket = "your-terraform-state-bucket"
  #   prefix = "jit-runners"
  # }
}
