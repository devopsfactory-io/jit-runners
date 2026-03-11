resource "aws_dynamodb_table" "runners" {
  name         = "${var.project_name}-runners"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "runner_id"

  attribute {
    name = "runner_id"
    type = "S"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  tags = {
    Name = "${var.project_name}-runners"
  }
}
