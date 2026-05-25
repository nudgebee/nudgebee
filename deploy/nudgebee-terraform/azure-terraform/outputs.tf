output "client_id" {
  value = module.client.client_id
}

output "client_secret" {
  value     = module.client.client_secret
  sensitive = true
}

output "sp_object_id" {
  value = module.client.service_principal_object_id
}
