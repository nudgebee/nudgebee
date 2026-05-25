
alter table "public"."dw_tables" alter column "db_type" set not null;

alter table "public"."dw_tables" drop constraint "dw_tables_table_name_schema_name_cloud_account_id_tenant_id_database_name_key";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."dw_tables" add column "last_dml_at" timestamptz
--  null;
