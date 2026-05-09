


alter table "public"."billing_usage_cost" add column "account_id" uuid
 null;

alter table "public"."billing_usage_cost"
  add constraint "billing_usage_cost_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update no action on delete no action;

alter table "public"."billing_usage_cost" drop constraint "billing_usage_cost_account_id_fkey";

CREATE UNIQUE INDEX "tenant_id, billing_date, service_name, name, account_id" on
  "public"."billing_usage_cost" using btree ("tenant_id", "billing_date", "service_name", "name", "account_id");
