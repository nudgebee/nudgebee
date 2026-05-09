
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 4.0.0"
    }
  }
}

provider "google" {
  project = var.gcp_project_id
  # The credentials are provided via the TF_VAR_gcp_credentials environment variable
  # which is automatically picked up by the provider if the variable is defined.
  # We define it in variables.tf to make this explicit.
}

locals {
  // Create a unique but predictable name for the service account
  service_account_id = "nudgebee-collector-${substr(md5(var.gcp_project_id), 0, 8)}"
}

// Explicitly enable required APIs to ensure they are available.
resource "google_project_service" "iam" {
  service = "iam.googleapis.com"
}

resource "google_project_service" "iamcredentials" {
  service = "iamcredentials.googleapis.com"
}

resource "google_project_service" "bigquery" {
  service = "bigquery.googleapis.com"
}

# Service account for Nudgebee
resource "google_service_account" "nudgebee_service_account" {
  project      = var.gcp_project_id
  account_id   = local.service_account_id
  display_name = "Nudgebee Collector Service Account"
  depends_on = [
    google_project_service.iam
  ]
}

# Granting roles to the service account
# Grant least-privilege roles for reading BigQuery data.
resource "google_project_iam_member" "nudgebee_bigquery_dataviewer" {
  project = var.gcp_project_id
  role    = "roles/bigquery.dataViewer"
  member  = "serviceAccount:${google_service_account.nudgebee_service_account.email}"
}

resource "google_project_iam_member" "nudgebee_bigquery_jobuser" {
  project = var.gcp_project_id
  role    = "roles/bigquery.jobUser"
  member  = "serviceAccount:${google_service_account.nudgebee_service_account.email}"
}

// --- Workload Identity Federation Resources ---

locals {
  // Create a unique but predictable name for the workload identity pool
  workload_identity_pool_id = "nudgebee-pool-${substr(md5(var.gcp_project_id), 0, 8)}"
}

// 3. Create a Workload Identity Pool
resource "google_iam_workload_identity_pool" "nudgebee_pool" {
  provider                  = "google"
  project                   = var.gcp_project_id
  workload_identity_pool_id = local.workload_identity_pool_id
  display_name              = "Nudgebee Identity Pool"
  description               = "Identity pool for Nudgebee collectors"
  depends_on = [
    google_project_service.iam
  ]
}

// 4. Create a Provider for AWS within the pool
resource "google_iam_workload_identity_pool_provider" "nudgebee_aws_provider" {
  provider                           = "google"
  project                            = var.gcp_project_id
  workload_identity_pool_id          = google_iam_workload_identity_pool.nudgebee_pool.workload_identity_pool_id
  workload_identity_pool_provider_id = "nudgebee-aws-provider" // A static ID for the provider
  display_name                       = "Nudgebee AWS Provider"
  description                        = "Trusts the Nudgebee AWS account"
  aws {
    account_id = var.aws_account_id
  }
}

# Create a service account key
resource "google_service_account_key" "nudgebee_service_account_key" {
  service_account_id = google_service_account.nudgebee_service_account.name
}
