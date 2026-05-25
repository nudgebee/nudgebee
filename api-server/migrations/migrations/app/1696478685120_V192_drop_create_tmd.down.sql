
alter table "public"."table_metadata_details" drop constraint "table_metadata_details_schema_name_cloud_account_id_tenant_id_database_name_table_name_table_id_key";
alter table "public"."table_metadata_details" add constraint "table_metadata_details_schema_name_cloud_account_id_tenant_id_database_name_table_name_key" unique ("schema_name", "cloud_account_id", "tenant_id", "database_name", "table_name");

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."table_metadata_details" add column "table_id" text
--  null;

ALTER TABLE "public"."table_metadata_details" ALTER COLUMN "id" drop default;

alter table "public"."table_metadata_details" drop constraint "table_metadata_details_cloud_account_id_tenant_id_database_name_table_name_schema_name_key";
alter table "public"."table_metadata_details" add constraint "table_metadata_details_table_created_at_cloud_account_id_tenant_id_database_name_table_name_schema_name_key" unique ("table_created_at", "cloud_account_id", "tenant_id", "database_name", "table_name", "schema_name");

DROP TABLE "public"."table_metadata_details";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."table_metadata_details";
