

DROP INDEX IF EXISTS "public"."tenant_id, billing_date, service_name, name, account_id";


alter table "public"."billing_usage_cost"
  add constraint "billing_usage_cost_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update no action on delete no action;

alter table "public"."billing_usage_cost" drop constraint "billing_usage_cost_account_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."billing_usage_cost" add column "account_id" uuid
--  null;
