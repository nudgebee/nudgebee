

alter table "public"."cloud_resource_query_perf" rename to "dw_queries";

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

CREATE OR REPLACE FUNCTION public.dw_query_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF dw_query_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
    (CASE WHEN 'tenant_id' = ANY(group_by) THEN tenant_id ELSE NULL END) AS tenant_id
    , (CASE WHEN 'account_id' = ANY(group_by) THEN account_id ELSE NULL END) AS account_id
    , (CASE WHEN 'resource_id' = ANY(group_by) THEN resource_id ELSE NULL END) AS resource_id
    , (CASE WHEN 'database_name' = ANY(group_by) THEN database_name ELSE NULL END) AS database_name
    , (CASE WHEN 'db_username' = ANY(group_by) THEN db_username ELSE NULL END) AS db_username
    , (CASE WHEN 'query_type' = ANY(group_by) THEN query_type ELSE NULL END) AS query_type
    , (CASE WHEN 'warehouse_name' = ANY(group_by) THEN tags ->> 'warehouse_name' ELSE NULL END) AS warehouse_name
	, AVG(query_exec_duration_micro) AS avg_query_exec_duration_micro
	, MAX(query_exec_duration_micro) AS max_query_exec_duration_micro
	, SUM(query_exec_duration_micro) AS sum_query_exec_duration_micro
	, AVG(bill) AS avg_bill
	, MAX(bill) AS max_bill
	, SUM(bill) AS sum_bill
	, COUNT(*) AS query_count
    FROM dw_queries
    WHERE
      ("hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL OR ("tenant_id" = ("hasura_session" ->> 'x-hasura-user-tenant-id')::uuid))
      AND
      ("where" #>> '{account_id,_eq}' IS NULL OR ("account_id" = ("where" #>> '{account_id,_eq}')::uuid))
      AND
      ("where" #>> '{resource_id,_eq}' IS NULL OR ("resource_id" = ("where" #>> '{resource_id,_eq}')::uuid))
      AND
      ("where" #>> '{database_name,_eq}' IS NULL OR ("database_name" = ("where" #>> '{database_name,_eq}')))
      AND
      ("where" #>> '{db_username,_eq}' IS NULL OR ("db_username" = ("where" #>> '{db_username,_eq}')))
      AND
      ("where" #>> '{query_type,_eq}' IS NULL OR ("query_type" = ("where" #>> '{query_type,_eq}')))
      AND
      ("where" #>> '{query_started_at,_gt}' IS NULL OR ("query_started_at" > ("where" #>> '{query_started_at,_gt}')::timestamp))
      AND
      ("where" #>> '{query_started_at,_lt}' IS NULL OR ("query_started_at" > ("where" #>> '{query_started_at,_lt}')::timestamp))
      AND
      ("where" #>> '{query_started_at,_le}' IS NULL OR ("query_started_at" <= ("where" #>> '{query_started_at,_le}')::timestamp))
      AND
      ("where" #>> '{query_started_at,_ge}' IS NULL OR ("query_started_at" >= ("where" #>> '{query_started_at,_ge}')::timestamp))
    GROUP BY
    (CASE WHEN 'tenant_id' = ANY(group_by) THEN tenant_id END),
    (CASE WHEN 'account_id' = ANY(group_by) THEN account_id END),
    (CASE WHEN 'resource_id' = ANY(group_by) THEN resource_id END),
    (CASE WHEN 'database_name' = ANY(group_by) THEN database_name END),
    (CASE WHEN 'db_username' = ANY(group_by) THEN db_username END),
    (CASE WHEN 'query_type' = ANY(group_by) THEN query_type END),
    (CASE WHEN 'warehouse_name' = ANY(group_by) THEN tags ->> 'warehouse_name' END)
$function$;

DROP FUNCTION "public"."cloud_resource_query_perf_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."json");

DROP VIEW "public"."cloud_resource_query_perf_groupings_type";
