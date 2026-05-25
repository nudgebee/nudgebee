
alter table "public"."dw_tables" add column "last_dml_at" timestamptz
 null;

alter table "public"."dw_tables" add constraint "dw_tables_table_name_schema_name_cloud_account_id_tenant_id_database_name_key" unique ("table_name", "schema_name", "cloud_account_id", "tenant_id", "database_name");

alter table "public"."dw_tables" alter column "db_type" drop not null;
