output "email" {
  value = google_service_account.this.email
}

output "name" {
  value = google_service_account.this.name
}

output "key_json" {
  value     = base64decode(google_service_account_key.this.private_key)
  sensitive = true
}
