
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."slo_config" add column "goal" numeric
--  null;

alter table "public"."slo_config" drop constraint "slo_config_name_workload_name_workload_namespace_key";

alter table "public"."slo_config" alter column "workload_name" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."slo_config" add column "workload_namespace" text
--  not null;

ALTER TABLE "public"."slo_config" ALTER COLUMN "created_at" TYPE timestamp with time zone;

ALTER TABLE "public"."slo_config" ALTER COLUMN "updated_at" TYPE timestamp with time zone;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."slo_config" add column "workload_name" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."slo_config" add column "enabled" boolean
--  null default 'true';
