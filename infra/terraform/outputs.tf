output "webhook_url" {
  description = "URL to configure as the GitHub App webhook endpoint"
  value       = "${aws_apigatewayv2_api.webhook.api_endpoint}/webhook"
}

output "webhook_lambda_arn" {
  description = "ARN of the webhook Lambda function"
  value       = aws_lambda_function.webhook.arn
}

output "scaleup_lambda_arn" {
  description = "ARN of the scale-up Lambda function"
  value       = aws_lambda_function.scaleup.arn
}

output "scaledown_lambda_arn" {
  description = "ARN of the scale-down Lambda function"
  value       = aws_lambda_function.scaledown.arn
}

output "dynamodb_table_name" {
  description = "DynamoDB table name for runner state"
  value       = aws_dynamodb_table.runners.name
}

output "sqs_queue_url" {
  description = "SQS queue URL for scale-up messages"
  value       = aws_sqs_queue.scaleup.url
}

output "runner_security_group_id" {
  description = "Security group ID for runner EC2 instances"
  value       = aws_security_group.runner.id
}

output "runner_instance_profile" {
  description = "IAM instance profile name for runner EC2 instances"
  value       = aws_iam_instance_profile.runner.name
}
