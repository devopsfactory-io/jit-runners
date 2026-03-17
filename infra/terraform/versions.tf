terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }

  # Configure your backend here.
  # backend "s3" {
  #   bucket         = "your-terraform-state-bucket"
  #   key            = "jit-runners/terraform.tfstate"
  #   region         = "us-east-1"
  #   dynamodb_table = "terraform-locks"
  #   encrypt        = true
  # }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      ManagedBy = "jit-runners"
      Project   = "jit-runners"
    }
  }
}
