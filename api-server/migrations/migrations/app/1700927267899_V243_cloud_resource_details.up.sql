
alter table "public"."cloud_resource_details" add constraint "cloud_resource_details_cloud_provider_resource_type_resource_region_service_name_key" unique ("cloud_provider", "resource_type", "resource_region", "service_name");

alter table "public"."cloud_resource_details" drop constraint "cloud_resource_details_cloud_provider_resource_type_resource_re";
alter table "public"."cloud_resource_details" add constraint "cloud_resource_details_service_name_resource_region_resource_type_cloud_provider_service_type_key" unique ("service_name", "resource_region", "resource_type", "cloud_provider", "service_type");

alter table "public"."cloud_resource_details" add column "attributes" jsonb
 null;
