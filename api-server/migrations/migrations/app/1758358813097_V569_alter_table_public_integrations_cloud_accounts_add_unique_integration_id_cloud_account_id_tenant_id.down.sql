
alter table "public"."integrations_cloud_accounts" drop constraint "integrations_cloud_accounts_integration_id_cloud_account_id_tenant_id_key";
alter table "public"."integrations_cloud_accounts" add constraint "integrations_cloud_accounts_integration_id_cloud_account_id_key" unique ("integration_id", "cloud_account_id");
