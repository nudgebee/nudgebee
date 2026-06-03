output "role_arn" {
  description = "Cross-account IAM role ARN"
  value       = aws_iam_role.cross_account_role.arn
}

output "bucket_name" {
  description = "S3 bucket for Cost and Usage Reports"
  value       = aws_s3_bucket.cost_usage_bucket.bucket
}
