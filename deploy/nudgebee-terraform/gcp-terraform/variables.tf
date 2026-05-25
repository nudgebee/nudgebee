variable "project_id" {
  type = string
}

variable "region" {
  type    = string
  default = "us-east1"
}

variable "sa_account_id" {
  type        = string
  description = "Service Account ID (user input during terraform apply)"
}

variable "sa_display_name" {
  type    = string
  default = "Terraform Generated Service Account"
}

variable "sa_project_roles" {
  type = list(string)
  default = [
    "roles/viewer",
    "roles/monitoring.viewer",
    "roles/bigquery.readSessionUser",
    "roles/bigquery.dataViewer"
  ]
}

# Hardcoded fields for your API request
variable "api_account_name" {
  type    = string
  default = "GCP_Prod_test_1"
}

variable "api_account_number" {
  type    = string
  default = "nudgebee-dev"
}

variable "api_account_type" {
  type    = string
  default = "cloud"
}

variable "api_cloud_provider" {
  type    = string
  default = "GCP"
}
