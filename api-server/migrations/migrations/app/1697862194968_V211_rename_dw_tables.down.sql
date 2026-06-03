
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE OR REPLACE VIEW "public"."dw_databases" AS
--  SELECT
--     dw_tables.tenant_id AS tenant_id,
--     dw_tables.cloud_account_id AS cloud_account_id,
--     dw_tables.database_name,
--     count(*) AS table_count,
--     sum(dw_tables.row_count) AS row_count,
--     sum(dw_tables.table_size) AS size,
--     sum(dw_tables.table_fail_safe_byte) AS fail_safe_size,
--     sum(dw_tables.time_travel_bytes) AS time_travel_size
--  FROM dw_tables
--  GROUP BY
--     dw_tables.tenant_id,
--     dw_tables.cloud_account_id,
--     dw_tables.database_name;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."dw_databases";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."table_size_details";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE OR REPLACE VIEW "public"."dw_databases" AS
--  SELECT dw_tables.database_name,
--     sum(dw_tables.row_count) AS table_count,
--     sum(dw_tables.table_size) AS active_size,
--     sum(dw_tables.table_fail_safe_byte) AS fail_safe_size,
--     sum(dw_tables.time_travel_bytes) AS time_travel_size
--    FROM dw_tables
--   GROUP BY dw_tables.database_name;

alter table "public"."dw_tables" rename to "table_metadata_details";
