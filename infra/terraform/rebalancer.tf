# Rebalancer Lambda (issue #62).
#
# Mirrors infra/cloudformation/template.yaml additions in the same change set.
# Periodic scheduler that re-publishes ScaleUpMessages to drain stranded
# queued GitHub Actions jobs.

resource "aws_lambda_function" "rebalancer" {
  function_name = "${var.project_name}-rebalancer"
  description   = "Periodic re-publisher of ScaleUpMessages to drain stranded queued GitHub Actions jobs."
  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["x86_64"]
  memory_size   = 256
  timeout       = 60

  reserved_concurrent_executions = 1

  s3_bucket = var.webhook_lambda_s3_bucket
  s3_key    = var.rebalancer_lambda_s3_key

  role = aws_iam_role.rebalancer_lambda.arn

  environment {
    variables = {
      GITHUB_APP_ID                     = var.github_app_id
      GITHUB_INSTALLATION_ID            = var.github_installation_id
      GITHUB_APP_PRIVATE_KEY_SECRET_ARN = var.private_key_arn
      GITHUB_APP_WEBHOOK_SECRET_ARN     = var.webhook_secret_arn
      DYNAMODB_TABLE_NAME               = aws_dynamodb_table.runners.name
      SQS_QUEUE_URL                     = aws_sqs_queue.scaleup.url
      REPOSITORY_FULL                   = var.repository_full
    }
  }

  tags = {
    Name = "${var.project_name}-rebalancer"
  }
}

resource "aws_cloudwatch_log_group" "rebalancer" {
  name              = "/aws/lambda/${var.project_name}-rebalancer"
  retention_in_days = 14
}

# --- IAM: Rebalancer Lambda ---

resource "aws_iam_role" "rebalancer_lambda" {
  name = "${var.project_name}-rebalancer-lambda"

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

resource "aws_iam_role_policy_attachment" "rebalancer_lambda_basic" {
  role       = aws_iam_role.rebalancer_lambda.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy" "rebalancer_lambda" {
  name = "${var.project_name}-rebalancer-lambda"
  role = aws_iam_role.rebalancer_lambda.id

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
        Resource = "${aws_cloudwatch_log_group.rebalancer.arn}:*"
      },
      {
        Effect   = "Allow"
        Action   = ["sqs:SendMessage"]
        Resource = aws_sqs_queue.scaleup.arn
      },
      {
        Effect   = "Allow"
        Action   = ["dynamodb:Scan"]
        Resource = aws_dynamodb_table.runners.arn
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

# --- EventBridge Scheduler: Rebalancer ---

resource "aws_scheduler_schedule" "rebalancer" {
  name       = "${var.project_name}-rebalancer"
  group_name = "default"

  flexible_time_window {
    mode = "OFF"
  }

  schedule_expression = "rate(1 minute)"

  target {
    arn      = aws_lambda_function.rebalancer.arn
    role_arn = aws_iam_role.scheduler.arn
  }
}
