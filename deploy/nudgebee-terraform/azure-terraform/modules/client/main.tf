resource "azuread_application" "app" {
  display_name = var.client_name
}

resource "azuread_service_principal" "sp" {
  client_id = azuread_application.app.client_id
}

resource "azuread_application_password" "secret" {
  application_object_id = azuread_application.app.object_id
  display_name          = "module-generated-secret"
}

data "azurerm_subscription" "sub" {
  subscription_id = var.subscription_id
}

data "azurerm_role_definition" "role" {
  name  = var.role_name
  scope = data.azurerm_subscription.sub.id
}

resource "azurerm_role_assignment" "assign" {
  scope              = data.azurerm_subscription.sub.id
  role_definition_id = data.azurerm_role_definition.role.id
  principal_id       = azuread_service_principal.sp.id
}
