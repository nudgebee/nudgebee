
alter table "public"."table_metadata_details" rename to "dw_tables";

CREATE OR REPLACE VIEW "public"."dw_databases" AS 
 SELECT dw_tables.database_name,
    sum(dw_tables.row_count) AS table_count,
    sum(dw_tables.table_size) AS active_size,
    sum(dw_tables.table_fail_safe_byte) AS fail_safe_size,
    sum(dw_tables.time_travel_bytes) AS time_travel_size
   FROM dw_tables
  GROUP BY dw_tables.database_name;

DROP VIEW "public"."table_size_details";

DROP VIEW "public"."dw_databases";

CREATE OR REPLACE VIEW "public"."dw_databases" AS 
 SELECT 
    dw_tables.tenant_id AS tenant_id,
    dw_tables.cloud_account_id AS cloud_account_id,
    dw_tables.database_name,
    count(*) AS table_count,
    sum(dw_tables.row_count) AS row_count,
    sum(dw_tables.table_size) AS size,
    sum(dw_tables.table_fail_safe_byte) AS fail_safe_size,
    sum(dw_tables.time_travel_bytes) AS time_travel_size
 FROM dw_tables
 GROUP BY 
    dw_tables.tenant_id,
    dw_tables.cloud_account_id,
    dw_tables.database_name;
