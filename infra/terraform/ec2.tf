# --- Security Group for Runner Instances ---

resource "aws_security_group" "runner" {
  name_prefix = "${var.project_name}-runner-"
  description = "Security group for jit-runners EC2 instances"
  vpc_id      = var.vpc_id

  # Egress only — runners need outbound HTTPS to GitHub and AWS APIs.
  egress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "HTTPS to GitHub and AWS APIs"
  }

  # Allow outbound HTTP for package managers (apt, yum).
  egress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "HTTP for package managers"
  }

  # Allow DNS.
  egress {
    from_port   = 53
    to_port     = 53
    protocol    = "udp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "DNS"
  }

  # No ingress rules — runners don't need inbound traffic.

  tags = {
    Name = "${var.project_name}-runner"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# --- IAM Role for Runner Instances ---

resource "aws_iam_role" "runner" {
  name = "${var.project_name}-runner"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ec2.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy" "runner" {
  name = "${var.project_name}-runner"
  role = aws_iam_role.runner.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["ec2:TerminateInstances"]
        Resource = "*"
        Condition = {
          StringEquals = {
            "ec2:ResourceTag/managed-by" = "jit-runners"
          }
        }
      },
    ]
  })
}

resource "aws_iam_instance_profile" "runner" {
  name = "${var.project_name}-runner"
  role = aws_iam_role.runner.name
}
