output "client_id" {
  value = azuread_application.app.application_id
}

output "client_secret" {
  value     = azuread_application_password.secret.value
  sensitive = true
}

output "service_principal_object_id" {
  value = azuread_service_principal.sp.id
}
