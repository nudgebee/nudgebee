output "api_payload" {
  value = {
    account_name  = var.api_account_name

    # FIXED — use the raw JSON from module, not reconstructed JSON
    access_secret = module.collector_service_account.key_json

    account_number = var.api_account_number
    account_type   = var.api_account_type
    cloud_provider = var.api_cloud_provider

    data = {
      project_id   = var.project_id
      client_email = jsondecode(module.collector_service_account.key_json).client_email
    }
  }
  sensitive = true
}
