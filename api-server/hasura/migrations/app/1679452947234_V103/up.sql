
alter table "public"."cloud_accounts" add constraint "cloud_accounts_tenant_account_name_key" unique ("tenant", "account_name");
