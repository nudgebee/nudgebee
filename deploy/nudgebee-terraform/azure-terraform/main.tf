module "client" {
  source           = "./modules/client"
  tenant_id        = var.tenant_id
  subscription_id  = var.subscription_id
  client_name      = var.client_name
  role_name        = var.role_name
}
