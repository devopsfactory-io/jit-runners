# --- Webhook Lambda ---

resource "aws_lambda_function" "webhook" {
  function_name = "${var.project_name}-webhook"
  handler       = "webhook"
  runtime       = "provided.al2023"
  architectures = ["x86_64"]
  memory_size   = 128
  timeout       = 25

  s3_bucket = var.webhook_lambda_s3_bucket
  s3_key    = var.webhook_lambda_s3_key

  role = aws_iam_role.webhook_lambda.arn

  environment {
    variables = {
      GITHUB_APP_ID                    = var.github_app_id
      GITHUB_APP_WEBHOOK_SECRET_ARN    = var.webhook_secret_arn
      GITHUB_APP_PRIVATE_KEY_SECRET_ARN = var.private_key_arn
      SQS_QUEUE_URL                    = aws_sqs_queue.scaleup.url
      DYNAMODB_TABLE_NAME              = aws_dynamodb_table.runners.name
    }
  }

  tags = {
    Name = "${var.project_name}-webhook"
  }
}

resource "aws_cloudwatch_log_group" "webhook" {
  name              = "/aws/lambda/${var.project_name}-webhook"
  retention_in_days = 14
}

# --- Scale-Up Lambda ---

resource "aws_lambda_function" "scaleup" {
  function_name = "${var.project_name}-scaleup"
  handler       = "scaleup"
  runtime       = "provided.al2023"
  architectures = ["x86_64"]
  memory_size   = 256
  timeout       = 60

  s3_bucket = var.webhook_lambda_s3_bucket
  s3_key    = var.scaleup_lambda_s3_key

  role = aws_iam_role.scaleup_lambda.arn

  environment {
    variables = {
      GITHUB_APP_ID                    = var.github_app_id
      GITHUB_APP_WEBHOOK_SECRET_ARN    = var.webhook_secret_arn
      GITHUB_APP_PRIVATE_KEY_SECRET_ARN = var.private_key_arn
      SQS_QUEUE_URL                    = aws_sqs_queue.scaleup.url
      DYNAMODB_TABLE_NAME              = aws_dynamodb_table.runners.name
      EC2_SUBNET_IDS                   = join(",", var.subnet_ids)
      EC2_SECURITY_GROUP_ID            = aws_security_group.runner.id
      EC2_IAM_INSTANCE_PROFILE         = aws_iam_instance_profile.runner.name
      EC2_DEFAULT_AMI                  = var.default_ami
      LABEL_MAPPINGS                   = var.label_mappings
    }
  }

  tags = {
    Name = "${var.project_name}-scaleup"
  }
}

resource "aws_lambda_event_source_mapping" "scaleup_sqs" {
  event_source_arn = aws_sqs_queue.scaleup.arn
  function_name    = aws_lambda_function.scaleup.arn
  batch_size       = 1
}

resource "aws_cloudwatch_log_group" "scaleup" {
  name              = "/aws/lambda/${var.project_name}-scaleup"
  retention_in_days = 14
}

# --- Scale-Down Lambda ---

resource "aws_lambda_function" "scaledown" {
  function_name = "${var.project_name}-scaledown"
  handler       = "scaledown"
  runtime       = "provided.al2023"
  architectures = ["x86_64"]
  memory_size   = 256
  timeout       = 60

  s3_bucket = var.webhook_lambda_s3_bucket
  s3_key    = var.scaledown_lambda_s3_key

  role = aws_iam_role.scaledown_lambda.arn

  environment {
    variables = {
      GITHUB_APP_ID                    = var.github_app_id
      GITHUB_APP_WEBHOOK_SECRET_ARN    = var.webhook_secret_arn
      GITHUB_APP_PRIVATE_KEY_SECRET_ARN = var.private_key_arn
      SQS_QUEUE_URL                    = aws_sqs_queue.scaleup.url
      DYNAMODB_TABLE_NAME              = aws_dynamodb_table.runners.name
      STALE_THRESHOLD_MINUTES          = tostring(var.stale_threshold_minutes)
      MAX_RUNNER_AGE_MINUTES           = tostring(var.max_runner_age_minutes)
    }
  }

  tags = {
    Name = "${var.project_name}-scaledown"
  }
}

resource "aws_cloudwatch_log_group" "scaledown" {
  name              = "/aws/lambda/${var.project_name}-scaledown"
  retention_in_days = 14
}

# --- IAM: Webhook Lambda ---

resource "aws_iam_role" "webhook_lambda" {
  name = "${var.project_name}-webhook-lambda"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "lambda.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy" "webhook_lambda" {
  name = "${var.project_name}-webhook-lambda"
  role = aws_iam_role.webhook_lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
        ]
        Resource = "${aws_cloudwatch_log_group.webhook.arn}:*"
      },
      {
        Effect   = "Allow"
        Action   = ["sqs:SendMessage"]
        Resource = aws_sqs_queue.scaleup.arn
      },
      {
        Effect = "Allow"
        Action = ["secretsmanager:GetSecretValue"]
        Resource = [
          var.webhook_secret_arn,
          var.private_key_arn,
        ]
      },
    ]
  })
}

# --- IAM: Scale-Up Lambda ---

resource "aws_iam_role" "scaleup_lambda" {
  name = "${var.project_name}-scaleup-lambda"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "lambda.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy" "scaleup_lambda" {
  name = "${var.project_name}-scaleup-lambda"
  role = aws_iam_role.scaleup_lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
        ]
        Resource = "${aws_cloudwatch_log_group.scaleup.arn}:*"
      },
      {
        Effect = "Allow"
        Action = [
          "sqs:ReceiveMessage",
          "sqs:DeleteMessage",
          "sqs:GetQueueAttributes",
        ]
        Resource = aws_sqs_queue.scaleup.arn
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:PutItem",
          "dynamodb:GetItem",
          "dynamodb:UpdateItem",
        ]
        Resource = aws_dynamodb_table.runners.arn
      },
      {
        Effect = "Allow"
        Action = [
          "ec2:RunInstances",
          "ec2:CreateTags",
        ]
        Resource = "*"
      },
      {
        Effect   = "Allow"
        Action   = ["iam:PassRole"]
        Resource = aws_iam_role.runner.arn
      },
      {
        Effect = "Allow"
        Action = ["secretsmanager:GetSecretValue"]
        Resource = [
          var.webhook_secret_arn,
          var.private_key_arn,
        ]
      },
    ]
  })
}

# --- IAM: Scale-Down Lambda ---

resource "aws_iam_role" "scaledown_lambda" {
  name = "${var.project_name}-scaledown-lambda"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "lambda.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy" "scaledown_lambda" {
  name = "${var.project_name}-scaledown-lambda"
  role = aws_iam_role.scaledown_lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
        ]
        Resource = "${aws_cloudwatch_log_group.scaledown.arn}:*"
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:Scan",
          "dynamodb:UpdateItem",
        ]
        Resource = aws_dynamodb_table.runners.arn
      },
      {
        Effect = "Allow"
        Action = [
          "ec2:DescribeInstances",
          "ec2:TerminateInstances",
        ]
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = ["secretsmanager:GetSecretValue"]
        Resource = [
          var.webhook_secret_arn,
          var.private_key_arn,
        ]
      },
    ]
  })
}
