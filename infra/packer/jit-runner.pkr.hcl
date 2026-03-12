packer {
  required_plugins {
    amazon = {
      version = ">= 1.3.0"
      source  = "github.com/hashicorp/amazon"
    }
  }
}

source "amazon-ebs" "jit-runner" {
  region        = var.aws_region
  instance_type = var.instance_type
  ssh_username  = "ec2-user"
  ami_name      = "${var.ami_name_prefix}-${var.runner_version}-{{timestamp}}"
  ami_regions   = var.ami_regions

  # Publish to AWS Community AMI catalog
  ami_groups = ["all"]

  source_ami_filter {
    filters = {
      name                = "al2023-ami-*-x86_64"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    owners      = ["amazon"]
    most_recent = true
  }

  dynamic "subnet_filter" {
    for_each = var.subnet_id == "" ? [1] : []
    content {
      filters = {
        "default-for-az" = "true"
      }
      most_free = true
    }
  }

  subnet_id = var.subnet_id != "" ? var.subnet_id : null

  tags = {
    Name             = "${var.ami_name_prefix}-v${var.runner_version}"
    "runner-version" = var.runner_version
    "project"        = "jit-runners"
    "source"         = "github.com/devopsfactory-io/jit-runners"
    "built-by"       = "packer"
  }

  run_tags = {
    Name = "packer-jit-runner-build"
  }
}

build {
  sources = ["source.amazon-ebs.jit-runner"]

  # Base runner setup (dependencies, user, runner agent)
  provisioner "shell" {
    script = "scripts/setup-runner.sh"
    environment_vars = [
      "RUNNER_VERSION=${var.runner_version}",
    ]
  }

  # Optional: user-provided extra setup script
  # Pass -var 'extra_script=scripts/my-custom.sh' to packer build
  provisioner "shell" {
    inline = var.extra_script != "" ? ["chmod +x /tmp/extra-setup.sh && /tmp/extra-setup.sh"] : ["echo 'No extra script provided, skipping.'"]
  }

  post-processor "manifest" {
    output     = "manifest.json"
    strip_path = true
  }
}
