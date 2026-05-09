
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent" add column "version" text
--  null;

DROP INDEX IF EXISTS "public"."cloud_resource_metrics_resourceid";


DROP INDEX IF EXISTS "public"."agent_access_key";

DROP INDEX IF EXISTS "public"."cloud_resource_query_perf_tenant_id_resource_id_group";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."agent" add column "last_synced_at" timestamp
--  null;
