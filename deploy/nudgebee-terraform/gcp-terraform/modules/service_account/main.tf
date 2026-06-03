resource "google_service_account" "this" {
  account_id   = var.account_id
  display_name = var.display_name
}

resource "google_project_iam_member" "this" {
  for_each = toset(var.project_roles)

  project = var.project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.this.email}"
}

# Create JSON key
resource "google_service_account_key" "this" {
  service_account_id = google_service_account.this.name

  private_key_type = "TYPE_GOOGLE_CREDENTIALS_FILE"

  keepers = {
    email = google_service_account.this.email
  }
}
