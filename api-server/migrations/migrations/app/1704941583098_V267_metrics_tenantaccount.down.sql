
DROP INDEX IF EXISTS "public"."cloud_resource_metrics_tenantaccount";

DROP INDEX IF EXISTS "public"."cloud_resource_metrics_tenant";

alter table "public"."cloud_resource_metrics" drop constraint "cloud_resource_metrics_cloud_account_id_fkey";

alter table "public"."cloud_resource_metrics" drop constraint "cloud_resource_metrics_tenant_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_metrics" add column "tenant_id" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_metrics" add column "cloud_account_id" uuid
--  null;
