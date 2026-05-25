
DROP FUNCTION if exists "public"."dw_query_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."text", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."json");

DROP VIEW if exists "public"."dw_query_groupings_type";

ALTER TABLE "public"."dw_queries" ALTER COLUMN "query_exec_duration_micro" TYPE numeric;

ALTER TABLE "public"."dw_queries" ALTER COLUMN "bill_total_duration_micro" TYPE numeric;

ALTER TABLE "public"."dw_queries" ALTER COLUMN "query_planning_duration_micro" TYPE numeric;

ALTER TABLE "public"."dw_queries" ALTER COLUMN "query_returned_rows" TYPE numeric;

ALTER TABLE "public"."dw_queries" ALTER COLUMN "query_returned_bytes" TYPE numeric;

alter table "public"."dw_queries" alter column "query_exec_duration_micro" drop not null;

alter table "public"."dw_queries" alter column "bill_total_duration_micro" drop not null;

DROP FUNCTION if exists "public"."dw_query_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."text", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."int4", "pg_catalog"."text", "pg_catalog"."text", "pg_catalog"."json");

DROP VIEW if exists "public"."dw_query_groupings_type";

CREATE OR REPLACE VIEW "public"."dw_query_groupings_type" AS 
 SELECT dw_queries.tenant_id,
    dw_queries.account_id,
    dw_queries.resource_id,
    dw_queries.database_name,
    dw_queries.db_username,
    dw_queries.query_type,
    (dw_queries.tags ->> 'warehouse_name'::text) AS warehouse_name,
    avg(dw_queries.query_exec_duration_micro) AS avg_query_exec_duration_micro,
    max(dw_queries.query_exec_duration_micro) AS max_query_exec_duration_micro,
    sum(dw_queries.query_exec_duration_micro) AS sum_query_exec_duration_micro,
    avg(dw_queries.bill) AS avg_bill,
    max(dw_queries.bill) AS max_bill,
    sum(dw_queries.bill) AS sum_bill,
    count(*) AS query_count
   FROM dw_queries
  WHERE false
  GROUP BY dw_queries.tenant_id, dw_queries.account_id, dw_queries.resource_id, dw_queries.database_name, dw_queries.db_username, dw_queries.query_type, (dw_queries.tags ->> 'warehouse_name'::text);
