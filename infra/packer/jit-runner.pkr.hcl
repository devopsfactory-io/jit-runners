packer {
  required_plugins {
    amazon = {
      version = ">= 1.3.0"
      source  = "github.com/hashicorp/amazon"
    }
  }
}

source "amazon-ebs" "jit-runner" {
  region                      = var.aws_region
  instance_type               = var.instance_type
  ssh_username                = "ec2-user"
  associate_public_ip_address = true
  ssh_timeout                 = "10m"
  ami_name      = "${var.ami_name_prefix}-${var.jit_runners_version}-runner${var.runner_version}-{{timestamp}}"
  ami_regions   = var.ami_regions

  # Launch permissions: ["all"] for public (community AMI), [] for private (PR test builds)
  ami_groups = var.ami_groups

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

  launch_block_device_mappings {
    device_name           = "/dev/xvda"
    volume_size           = var.volume_size
    volume_type           = "gp3"
    delete_on_termination = true
  }

  tags = {
    Name                  = "${var.ami_name_prefix}-${var.jit_runners_version}-runner${var.runner_version}"
    "runner-version"      = var.runner_version
    "jit-runners-version" = var.jit_runners_version
    "project"             = "jit-runners"
    "source"              = "github.com/devopsfactory-io/jit-runners"
    "built-by"            = "packer"
    "tools"               = "git,docker,python3,node,go,awscli,kubectl,helm,gh,jq,yq,git-lfs,gcc,cmake,make"
  }

  run_tags = {
    Name = "packer-jit-runner-build"
  }
}

build {
  sources = ["source.amazon-ebs.jit-runner"]

  # Create destination directory for provisioning scripts
  provisioner "shell" {
    inline = ["mkdir -p /tmp/packer-scripts"]
  }

  # Upload all provisioning scripts to the remote instance
  provisioner "file" {
    source      = "scripts/"
    destination = "/tmp/packer-scripts"
  }

  # Full runner setup (system packages, Docker, languages, cloud tools, runner agent)
  provisioner "shell" {
    inline = ["chmod +x /tmp/packer-scripts/*.sh && bash /tmp/packer-scripts/setup-runner.sh"]
    environment_vars = [
      "RUNNER_VERSION=${var.runner_version}",
      "GO_VERSION=${var.go_version}",
      "NODE_MAJOR=${var.node_major_version}",
      "JIT_RUNNERS_VERSION=${var.jit_runners_version}",
    ]
  }

  # Optional: user-provided extra setup script
  # Pass -var 'extra_script=scripts/my-custom.sh' to packer build
  provisioner "shell" {
    inline = var.extra_script != "" ? [
      "chmod +x /tmp/packer-scripts/$(basename '${var.extra_script}')",
      "/tmp/packer-scripts/$(basename '${var.extra_script}')",
    ] : ["echo 'No extra script provided, skipping.'"]
  }

  # Validate that all critical tools were installed
  provisioner "shell" {
    inline = [
      "echo '=== jit-runners: validating installed tools ==='",
      "git --version",
      "docker --version",
      "docker compose version",
      "docker buildx version",
      "python3 --version",
      "node --version",
      "/usr/local/go/bin/go version",
      "aws --version",
      "kubectl version --client -o json | jq -r '.clientVersion.gitVersion'",
      "helm version --short",
      "gh --version",
      "jq --version",
      "yq --version",
      "gcc --version | head -1",
      "cmake --version | head -1",
      "make --version | head -1",
      "git lfs version",
      "cat /opt/jit-runner-manifest.txt",
      "echo '=== jit-runners: all tools validated ==='",
    ]
  }

  post-processor "manifest" {
    output     = "manifest.json"
    strip_path = true
  }
}
