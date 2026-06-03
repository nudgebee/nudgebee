
CREATE TABLE "public"."cloud_resource_metrics_capacity" ("cloud_provider" text NOT NULL, "service_name" text NOT NULL, "service_type" text NOT NULL, "resource_type" text NOT NULL, "metrics_name" text NOT NULL, "metrics_value" text NOT NULL, "id" uuid NOT NULL DEFAULT gen_random_uuid(), PRIMARY KEY ("id") , UNIQUE ("cloud_provider", "service_name", "service_type", "resource_type", "metrics_name"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."cloud_resource_metrics_capacity" add column "resource_region" text
 null;

alter table "public"."cloud_resource_metrics_capacity" drop constraint "cloud_resource_metrics_capaci_cloud_provider_service_name_s_key";
alter table "public"."cloud_resource_metrics_capacity" add constraint "cloud_resource_metrics_capacity_cloud_provider_metrics_name_resource_type_service_type_service_name_resource_region_key" unique ("cloud_provider", "metrics_name", "resource_type", "service_type", "service_name", "resource_region");

alter table "public"."cloud_resource_metrics_capacity" add column "resource_cost" float8
 not null default '0.0';

alter table "public"."cloud_resource_metrics_capacity" drop column "metrics_name" cascade;

alter table "public"."cloud_resource_metrics_capacity" drop column "metrics_value" cascade;

alter table "public"."cloud_resource_metrics_capacity" add column "resource_capacity" jsonb
 not null default '{}';

alter table "public"."cloud_resource_metrics_capacity" rename to "cloud_resource_details";

CREATE OR REPLACE VIEW "public"."alert_metrices_view" AS 
SELECT rm."timestamp",
    rm.metric,
    rm.value,
	rm.cloud_resource_id AS resource_id,
	cr.service_name AS resource_service_name,
	cr.meta AS resource_meta,
	cr.tags AS resource_tags,
	cr.name AS resource_name,
	cr.account AS account_id,
	cr.tenant AS tenant_id,
	crd.resource_capacity as resource_capacity
FROM (cloud_resource_metrics rm
LEFT JOIN cloud_resourses cr ON ((rm.cloud_resource_id = cr.id)))
left join cloud_resource_details crd on crd.resource_type = cr.meta ->> 'flavor';
