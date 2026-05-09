
DROP table "public"."table_metadata_details";

CREATE TABLE "public"."table_metadata_details" ("id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "table_name" text, "schema_name" text, "database_name" text, "row_count" numeric, "table_size" numeric, "table_created_at" timestamptz, "last_altered_at" timestamptz, "last_ddl_at" timestamptz, "table_type" text, "db_type" text NOT NULL, "table_fail_safe_byte" numeric, "time_travel_bytes" numeric, "table_reclone_bytes" numeric, "table_deleted" boolean, "table_dropped" timestamptz, PRIMARY KEY ("id") , FOREIGN KEY ("db_type") REFERENCES "public"."db_type"("value") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("cloud_account_id", "tenant_id", "database_name", "table_name", "schema_name", "table_created_at"));

alter table "public"."table_metadata_details" drop constraint "table_metadata_details_cloud_account_id_tenant_id_database__key";
alter table "public"."table_metadata_details" add constraint "table_metadata_details_cloud_account_id_tenant_id_database_name_table_name_schema_name_key" unique ("cloud_account_id", "tenant_id", "database_name", "table_name", "schema_name");

alter table "public"."table_metadata_details" alter column "id" set default gen_random_uuid();

alter table "public"."table_metadata_details" add column "table_id" text
 null;

alter table "public"."table_metadata_details" drop constraint "table_metadata_details_cloud_account_id_tenant_id_database_name";
alter table "public"."table_metadata_details" add constraint "table_metadata_details_schema_name_cloud_account_id_tenant_id_database_name_table_name_table_id_key" unique ("schema_name", "cloud_account_id", "tenant_id", "database_name", "table_name", "table_id");
