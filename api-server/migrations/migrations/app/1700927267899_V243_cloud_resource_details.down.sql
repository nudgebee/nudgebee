
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resource_details" add column "attributes" jsonb
--  null;

alter table "public"."cloud_resource_details" drop constraint "cloud_resource_details_service_name_resource_region_resource_type_cloud_provider_service_type_key";
alter table "public"."cloud_resource_details" add constraint "cloud_resource_details_service_name_resource_region_resource_type_cloud_provider_key" unique ("service_name", "resource_region", "resource_type", "cloud_provider");

alter table "public"."cloud_resource_details" drop constraint "cloud_resource_details_cloud_provider_resource_type_resource_region_service_name_key";
