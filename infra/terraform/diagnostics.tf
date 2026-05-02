# Diagnostic logging (issue #55).
#
# Mirrors infra/cloudformation/template.yaml additions in the same change set.

resource "aws_cloudwatch_log_group" "runner_agent" {
  name              = "/jit-runners/runner-agent"
  retention_in_days = 14
}

resource "aws_cloudwatch_log_group" "runner_userdata" {
  name              = "/jit-runners/userdata"
  retention_in_days = 14
}

resource "aws_cloudwatch_log_metric_filter" "silent_failure" {
  name           = "JitNoJobPickup"
  log_group_name = aws_cloudwatch_log_group.runner_userdata.name
  pattern        = "JIT_NO_JOB_PICKUP"

  metric_transformation {
    name          = "SilentFailures"
    namespace     = "JitRunners/RunnerAgent"
    value         = "1"
    default_value = "0"
  }
}

resource "aws_ssm_parameter" "runner_log_level" {
  name        = "/jit-runners/runner-log-level"
  type        = "String"
  value       = "info"
  description = "Runner-agent verbosity (info|debug). Read by scaleup on each invocation."

  lifecycle {
    # Operators flip this manually during incidents (aws ssm put-parameter).
    # Routine `terraform apply` must NOT silently revert their change.
    ignore_changes = [value]
  }
}

# RunnerRole — append CloudWatch Logs permissions for the new groups.
# Defined as a separate inline policy attached to the existing role, so it
# coexists with the SelfTerminate policy already in ec2.tf.
resource "aws_iam_role_policy" "runner_cloudwatch_logs" {
  name = "${aws_iam_role.runner.name}-cw-logs"
  role = aws_iam_role.runner.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogStream",
          "logs:PutLogEvents",
          "logs:DescribeLogStreams",
        ]
        Resource = [
          aws_cloudwatch_log_group.runner_agent.arn,
          aws_cloudwatch_log_group.runner_userdata.arn,
          "${aws_cloudwatch_log_group.runner_agent.arn}:log-stream:*",
          "${aws_cloudwatch_log_group.runner_userdata.arn}:log-stream:*",
        ]
      },
    ]
  })
}

# ScaleUpLambdaRole — read /jit-runners/runner-log-level from SSM.
resource "aws_iam_role_policy" "scaleup_ssm_read_log_level" {
  name = "${aws_iam_role.scaleup_lambda.name}-ssm-log-level"
  role = aws_iam_role.scaleup_lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = "ssm:GetParameter"
        Resource = aws_ssm_parameter.runner_log_level.arn
      },
    ]
  })
}
