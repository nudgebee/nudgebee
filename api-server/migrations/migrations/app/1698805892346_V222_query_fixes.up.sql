

alter table "public"."dw_queries" alter column "bill_interval_from" drop not null;

alter table "public"."dw_queries" alter column "bill_interval_to" drop not null;

alter table "public"."dw_queries" add column "query_remote_ip" text
 null;

alter table "public"."dw_queries" alter column "query_id" drop not null;

CREATE OR REPLACE FUNCTION public.dw_query_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, date_unit text DEFAULT 'day'::text, "limit" integer DEFAULT 100, "offset" integer DEFAULT 0, sort_by text DEFAULT 'query_started_at'::text, sort_order text DEFAULT 'asc'::text, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF dw_query_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
  SELECT
    (CASE WHEN 'tenant_id' = ANY(group_by) THEN tenant_id ELSE NULL END) AS tenant_id,
    (CASE WHEN 'account_id' = ANY(group_by) THEN account_id ELSE NULL END) AS account_id,
    (CASE WHEN 'resource_id' = ANY(group_by) THEN resource_id ELSE NULL END) AS resource_id,
    (CASE WHEN 'database_name' = ANY(group_by) THEN database_name ELSE NULL END) AS database_name,
    (CASE WHEN 'db_username' = ANY(group_by) THEN db_username ELSE NULL END) AS db_username,
    (CASE WHEN 'query_type' = ANY(group_by) THEN query_type ELSE NULL END) AS query_type,
    (CASE WHEN 'query_started_at' = ANY(group_by) THEN date_trunc(date_unit, query_started_at) ELSE NULL END) AS query_started_at,
    (CASE WHEN 'query_status' = ANY(group_by) THEN query_status ELSE NULL END) AS query_status,
    (CASE WHEN 'warehouse_name' = ANY(group_by) THEN tags ->> 'warehouse_name' ELSE NULL END) AS warehouse_name,
    (CASE WHEN 'query_normalized' = ANY(group_by) THEN query_normalized ELSE NULL END) AS query_normalized,
    (CASE WHEN 'query_normalized_md5' = ANY(group_by) THEN query_normalized_md5 ELSE NULL END) AS query_normalized_md5,
    (CASE WHEN 'query_text' = ANY(group_by) THEN query_text ELSE NULL END) AS query_text,
    avg(dw_queries.query_exec_duration_micro) AS avg_query_exec_duration_micro,
    max(dw_queries.query_exec_duration_micro) AS max_query_exec_duration_micro,
    sum(dw_queries.query_exec_duration_micro) AS sum_query_exec_duration_micro,
    avg(dw_queries.bill) AS avg_bill,
    max(dw_queries.bill) AS max_bill,
    sum(dw_queries.bill) AS sum_bill,
    avg(dw_queries.bytes_spilled_locally) as avg_bytes_spilled_locally,
    max(dw_queries.bytes_spilled_locally) as max_bytes_spilled_locally,
    sum(dw_queries.bytes_spilled_locally) as sum_bytes_spilled_locally,
    avg(dw_queries.bytes_spilled_remotely) as avg_bytes_spilled_remotely,
    max(dw_queries.bytes_spilled_remotely) as max_bytes_spilled_remotely,
    sum(dw_queries.bytes_spilled_remotely) as sum_bytes_spilled_remotely,
    avg(dw_queries.bytes_scanned) as avg_bytes_scanned,
    max(dw_queries.bytes_scanned) as max_bytes_scanned,
    sum(dw_queries.bytes_scanned) as sum_bytes_scanned,
    avg(dw_queries.partitions_scanned) as avg_partitions_scanned,
    max(dw_queries.partitions_scanned) as max_partitions_scanned,
    sum(dw_queries.partitions_scanned) as sum_partitions_scanned,
    avg(dw_queries.query_planning_duration_micro) as avg_query_planning_duration_micro,
    max(dw_queries.query_planning_duration_micro) as max_query_planning_duration_micro,
    sum(dw_queries.query_planning_duration_micro) as sum_query_planning_duration_micro,
    avg(dw_queries.query_queue_duration_micro) as avg_query_queue_duration_micro,
    max(dw_queries.query_queue_duration_micro) as max_query_queue_duration_micro,
    sum(dw_queries.query_queue_duration_micro) as sum_query_queue_duration_micro,
    avg(dw_queries.query_returned_bytes) as avg_query_returned_bytes,
    max(dw_queries.query_returned_bytes) as max_query_returned_bytes,
    sum(dw_queries.query_returned_bytes) as sum_query_returned_bytes,
    avg(dw_queries.query_returned_rows) as avg_query_returned_rows,
    max(dw_queries.query_returned_rows) as max_query_returned_rows,
    sum(dw_queries.query_returned_rows) as sum_query_returned_rows,
    avg(dw_queries.rpu) as avg_rpu,
    max(dw_queries.rpu) as max_rpu,
    sum(dw_queries.rpu) as sum_rpu,
    max(dw_queries.query_started_at) as max_query_started_at,
    min(dw_queries.query_started_at) as min_query_started_at,
    count(*) AS query_count
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
      ("where" #>> '{warehouse_name,_eq}' IS NULL OR (tags ->> 'warehouse_name' = ("where" #>> '{warehouse_name,_eq}')))
      AND
      ("where" #>> '{query_type,_eq}' IS NULL OR ("query_type" = ("where" #>> '{query_type,_eq}')))
      and
      ("where" #>> '{query_status,_eq}' IS NULL OR ("query_status" = ("where" #>> '{query_status,_eq}')))
      AND
      ("where" #>> '{query_normalized,_eq}' IS NULL OR ("query_normalized" = ("where" #>> '{query_normalized,_eq}')))
      AND
      ("where" #>> '{query_normalized_md5,_eq}' IS NULL OR ("query_normalized_md5" = ("where" #>> '{query_normalized_md5,_eq}')))
      AND
      ("where" #>> '{query_text,_eq}' IS NULL OR ("query_text" = ("where" #>> '{query_text,_eq}')))
      AND
      ("where" #>> '{query_started_at,_gt}' IS NULL OR ("query_started_at" > ("where" #>> '{query_started_at,_gt}')::timestamp))
      AND
      ("where" #>> '{query_started_at,_lt}' IS NULL OR ("query_started_at" > ("where" #>> '{query_started_at,_lt}')::timestamp))
      AND
      ("where" #>> '{query_started_at,_le}' IS NULL OR ("query_started_at" <= ("where" #>> '{query_started_at,_le}')::timestamp))
      AND
      ("where" #>> '{query_started_at,_ge}' IS NULL OR ("query_started_at" >= ("where" #>> '{query_started_at,_ge}')::timestamp))
      AND
      ("where" #>> '{query_started_at,_between}' IS NULL OR (("query_started_at" >= ("where" #>> '{query_started_at,_between,_ge}')::timestamp) and ("query_started_at" <= ("where" #>> '{query_started_at,_between,_le}')::timestamp)))
  GROUP BY
    (CASE WHEN 'tenant_id' = ANY(group_by) THEN tenant_id END),
    (CASE WHEN 'account_id' = ANY(group_by) THEN account_id END),
    (CASE WHEN 'resource_id' = ANY(group_by) THEN resource_id END),
    (CASE WHEN 'database_name' = ANY(group_by) THEN database_name END),
    (CASE WHEN 'db_username' = ANY(group_by) THEN db_username END),
    (CASE WHEN 'query_type' = ANY(group_by) THEN query_type END),
    (CASE WHEN 'query_status' = ANY(group_by) THEN query_status END),
    (CASE WHEN 'query_normalized' = ANY(group_by) THEN query_normalized END),
    (CASE WHEN 'query_normalized_md5' = ANY(group_by) THEN query_normalized_md5 END),
    (CASE WHEN 'query_text' = ANY(group_by) THEN query_text END),
    (CASE WHEN 'warehouse_name' = ANY(group_by) THEN tags ->> 'warehouse_name' END),
    (CASE WHEN 'query_started_at' = ANY(group_by) THEN date_trunc(date_unit, query_started_at) END)
  ORDER BY 
    (case when sort_by = 'query_started_at' and sort_order = 'asc' then max(date_trunc(date_unit, query_started_at)) end) asc,
    (case when sort_by = 'query_started_at' and sort_order = 'desc' then max(date_trunc(date_unit, query_started_at)) end) desc,
    (case when sort_by = 'query_count' and sort_order = 'asc' then count(*) end) asc,
    (case when sort_by = 'query_count' and sort_order = 'desc' then count(*) end) desc,
    (case when sort_by = 'sum_rpu' and sort_order = 'asc' then sum(dw_queries.rpu) end) asc,
    (case when sort_by = 'sum_rpu' and sort_order = 'desc' then sum(dw_queries.rpu) end) desc,
    (case when sort_by = 'sum_query_returned_rows' and sort_order = 'asc' then sum(dw_queries.query_returned_rows) end) asc,
    (case when sort_by = 'sum_query_returned_rows' and sort_order = 'desc' then sum(dw_queries.query_returned_rows) end) desc,
    (case when sort_by = 'sum_partitions_scanned' and sort_order = 'asc' then sum(dw_queries.partitions_scanned) end) asc,
    (case when sort_by = 'sum_partitions_scanned' and sort_order = 'desc' then sum(dw_queries.partitions_scanned) end) desc,
    (case when sort_by = 'sum_bytes_spilled_remotely' and sort_order = 'asc' then sum(dw_queries.bytes_spilled_remotely) end) asc,
    (case when sort_by = 'sum_bytes_spilled_remotely' and sort_order = 'desc' then sum(dw_queries.bytes_spilled_remotely) end) desc,
    (case when sort_by = 'sum_bill' and sort_order = 'asc' then sum(dw_queries.bill) end) asc,
    (case when sort_by = 'sum_bill' and sort_order = 'desc' then sum(dw_queries.bill) end) desc,
    (case when sort_by = 'sum_query_returned_bytes' and sort_order = 'asc' then sum(dw_queries.query_returned_bytes) end) asc,
    (case when sort_by = 'sum_query_returned_bytes' and sort_order = 'desc' then sum(dw_queries.query_returned_bytes) end) desc,
    (case when sort_by = 'sum_query_planning_duration_micro' and sort_order = 'asc' then sum(dw_queries.query_planning_duration_micro) end) asc,
    (case when sort_by = 'sum_query_planning_duration_micro' and sort_order = 'desc' then sum(dw_queries.query_planning_duration_micro) end) desc,
    (case when sort_by = 'sum_bytes_scanned' and sort_order = 'asc' then sum(dw_queries.bytes_scanned) end) asc,
    (case when sort_by = 'sum_bytes_scanned' and sort_order = 'desc' then sum(dw_queries.bytes_scanned) end) desc,
    (case when sort_by = 'sum_query_exec_duration_micro' and sort_order = 'asc' then sum(dw_queries.query_exec_duration_micro) end) asc,
    (case when sort_by = 'sum_query_exec_duration_micro' and sort_order = 'desc' then sum(dw_queries.query_exec_duration_micro) end) desc,
    (case when sort_by = 'sum_bytes_spilled_remotely' and sort_order = 'asc' then sum(dw_queries.bytes_spilled_remotely) end) asc,
    (case when sort_by = 'sum_bytes_spilled_remotely' and sort_order = 'desc' then sum(dw_queries.bytes_spilled_remotely) end) desc
  LIMIT "limit" OFFSET "offset";
$function$;
