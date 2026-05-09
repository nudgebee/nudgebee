
variable "gcp_project_id" {
  description = "The GCP project ID to provision resources in."
  type        = string
}

variable "gcp_credentials" {
  description = "The JSON credentials for a GCP service account with permissions to run this Terraform."
  type        = string
  sensitive   = true
}

variable "nudgebee_tenant_id" {
  description = "The Nudgebee tenant ID."
  type        = string
}

variable "nudgebee_account_name" {
  description = "The Nudgebee account name."
  type        = string
}

variable "aws_account_id" {
  description = "The AWS Account ID where the Nudgebee collector is running. Required for Workload Identity Federation."
  type        = string
}
