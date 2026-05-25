
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE OR REPLACE VIEW "public"."alert_metrices_view" AS
-- SELECT rm."timestamp",
--     rm.metric,
--     rm.value,
-- 	rm.cloud_resource_id AS resource_id,
-- 	cr.service_name AS resource_service_name,
-- 	cr.meta AS resource_meta,
-- 	cr.tags AS resource_tags,
-- 	cr.name AS resource_name,
-- 	cr.account AS account_id,
-- 	cr.tenant AS tenant_id,
-- 	crd.resource_capacity as resource_capacity
-- FROM (cloud_resource_metrics rm
-- LEFT JOIN cloud_resourses cr ON ((rm.cloud_resource_id = cr.id)))
-- left join cloud_resource_details crd on crd.resource_type = cr.meta ->> 'flavor';

alter table "public"."cloud_resource_details" rename to "cloud_resource_metrics_capacity";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_metrics_capacity" add column "resource_capacity" jsonb
--  not null default '{}';

alter table "public"."cloud_resource_metrics_capacity" alter column "metrics_value" drop not null;
alter table "public"."cloud_resource_metrics_capacity" add column "metrics_value" text;

alter table "public"."cloud_resource_metrics_capacity" add constraint "cloud_resource_metrics_capacity_cloud_provider_metrics_name_res" unique (metrics_name, resource_region, service_name, service_type, cloud_provider, resource_type);
alter table "public"."cloud_resource_metrics_capacity" alter column "metrics_name" drop not null;
alter table "public"."cloud_resource_metrics_capacity" add column "metrics_name" text;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_metrics_capacity" add column "resource_cost" float8
--  not null default '0.0';

alter table "public"."cloud_resource_metrics_capacity" drop constraint "cloud_resource_metrics_capacity_cloud_provider_metrics_name_resource_type_service_type_service_name_resource_region_key";
alter table "public"."cloud_resource_metrics_capacity" add constraint "cloud_resource_metrics_capacity_cloud_provider_metrics_name_resource_type_service_type_service_name_key" unique ("cloud_provider", "metrics_name", "resource_type", "service_type", "service_name");

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_metrics_capacity" add column "resource_region" text
--  null;

DROP TABLE "public"."cloud_resource_metrics_capacity";
