
// Output for Workload Identity Federation (preferred method)
output "workload_identity_pool_id" {
  description = "The ID of the Workload Identity Pool."
  value       = google_iam_workload_identity_pool.nudgebee_pool.workload_identity_pool_id
}

output "workload_identity_provider_id" {
  description = "The ID of the Workload Identity Pool Provider for AWS."
  value       = google_iam_workload_identity_pool_provider.nudgebee_aws_provider.workload_identity_pool_provider_id
}

output "service_account_email" {
  description = "The email of the created GCP Service Account to be impersonated."
  value       = google_service_account.nudgebee_service_account.email
}

// Output for Service Account Key (fallback method)
output "service_account_key" {
  description = "The base64 encoded JSON key for the service account."
  value       = google_service_account_key.nudgebee_service_account_key.private_key // This is already base64 encoded
  sensitive   = true
}
