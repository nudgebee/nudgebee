
alter table "public"."cloud_resource_metrics" add column "cloud_account_id" uuid
 null;

alter table "public"."cloud_resource_metrics" add column "tenant_id" uuid
 null;

alter table "public"."cloud_resource_metrics"
  add constraint "cloud_resource_metrics_tenant_id_fkey"
  foreign key ("tenant_id")
  references "public"."tenant"
  ("id") on update restrict on delete restrict;

alter table "public"."cloud_resource_metrics"
  add constraint "cloud_resource_metrics_cloud_account_id_fkey"
  foreign key ("cloud_account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;

CREATE  INDEX "cloud_resource_metrics_tenant" on
  "public"."cloud_resource_metrics" using btree ("tenant_id");

CREATE  INDEX "cloud_resource_metrics_tenantaccount" on
  "public"."cloud_resource_metrics" using btree ("tenant_id", "cloud_account_id");
