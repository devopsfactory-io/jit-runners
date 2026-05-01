resource "aws_sqs_queue" "scaleup" {
  name                       = "${var.project_name}-scaleup"
  delay_seconds              = 0 # delay is set per-message by the webhook Lambda
  visibility_timeout_seconds = 120
  message_retention_seconds  = 3600 # 1 hour
  receive_wait_time_seconds  = 10

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.scaleup_dlq.arn
    maxReceiveCount     = 3
  })

  tags = {
    Name = "${var.project_name}-scaleup"
  }
}

resource "aws_sqs_queue" "scaleup_dlq" {
  name                      = "${var.project_name}-scaleup-dlq"
  message_retention_seconds = 1209600 # 14 days

  tags = {
    Name = "${var.project_name}-scaleup-dlq"
  }
}

resource "aws_sqs_queue" "lifecycle_dlq" {
  name                      = "${var.project_name}-lifecycle-dlq"
  message_retention_seconds = 1209600 # 14 days

  tags = {
    Name = "${var.project_name}-lifecycle-dlq"
  }
}

resource "aws_sqs_queue" "lifecycle" {
  name                       = "${var.project_name}-lifecycle"
  visibility_timeout_seconds = 90
  message_retention_seconds  = 86400 # 24 hours

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.lifecycle_dlq.arn
    maxReceiveCount     = 5
  })

  tags = {
    Name = "${var.project_name}-lifecycle"
  }
}
