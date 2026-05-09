output "role_arn" {
  description = "Cross-account IAM role ARN"
  value       = module.nudgebee.role_arn
}

output "bucket_name" {
  description = "S3 bucket for Cost and Usage Reports"
  value       = module.nudgebee.bucket_name
}
