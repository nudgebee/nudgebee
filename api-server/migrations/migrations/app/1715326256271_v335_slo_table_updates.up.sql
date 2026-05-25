
alter table "public"."slo_config" add column "enabled" boolean
 null default 'true';

alter table "public"."slo_config" add column "workload_name" text
 null;

ALTER TABLE "public"."slo_config" ALTER COLUMN "updated_at" TYPE timestamp;

ALTER TABLE "public"."slo_config" ALTER COLUMN "created_at" TYPE timestamp;

alter table "public"."slo_config" add column "workload_namespace" text
 not null;

alter table "public"."slo_config" alter column "workload_name" set not null;

alter table "public"."slo_config" add constraint "slo_config_name_workload_name_workload_namespace_key" unique ("name", "workload_name", "workload_namespace");

alter table "public"."slo_config" add column "goal" numeric
 null;
