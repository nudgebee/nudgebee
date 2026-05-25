module "collector_service_account" {
  source        = "./modules/service_account"
  project_id    = var.project_id
  account_id    = var.sa_account_id
  display_name  = var.sa_display_name
  project_roles = var.sa_project_roles
}

resource "local_file" "service_account_key_full" {
  filename  = "${path.module}/service-account-full.json"
  content   = module.collector_service_account.key_json
}

locals {
  api_account_details = {
    account_name  = var.api_account_name
    account_type  = var.api_account_type
    account_cloud = var.api_cloud_provider
  }
}
