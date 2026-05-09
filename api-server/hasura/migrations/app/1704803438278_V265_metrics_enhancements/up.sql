
CREATE OR REPLACE FUNCTION public.metrics_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF cloud_resource_metrics_cloud_resourses_cloud_accounts
 LANGUAGE sql
 STABLE
AS $function$
SELECT
     (CASE WHEN 'tenant_id' = ANY(group_by) THEN a.tenant ELSE NULL end ) AS tenant_id,
     (CASE WHEN 'account_name' = ANY(group_by) THEN a.account_name ELSE NULL END) AS account_name, 
     (CASE WHEN 'account_id' = ANY(group_by) THEN cr.account ELSE NULL END) AS account_id, 
     (CASE WHEN 'name' = ANY(group_by) THEN cr.name ELSE NULL end ) AS name,
     (CASE WHEN 'metric' = ANY(group_by) THEN crm.metric ELSE NULL END) AS metric,
     SUM(crm.value) AS sum_value
FROM cloud_resource_metrics crm LEFT JOIN cloud_resourses cr ON crm.cloud_resource_id = cr.id 
LEFT JOIN cloud_accounts a ON a.id = cr.account
WHERE 
    ("hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL OR ( a.tenant = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid))
    AND ("where" #>> '{metric,_eq}' IS NULL OR (crm.metric = ("where" #>> '{metric,_eq}')))
    AND ("where" #>> '{account_id,_eq}' IS NULL OR (cr.account = ("where" #>> '{account_id,_eq}') :: uuid))
    AND ("where" #>> '{resource_id,_eq}' IS NULL OR (cr.id = ("where" #>> '{resource_id,_eq}') :: uuid))
    AND ("where" #>> '{name,_eq}' IS NULL OR (cr.name = ("where" #>> '{name,_eq}')))
    AND ("where" #>> '{k8s_workload_name,_eq}' IS NULL OR (cr.tags ->> 'controller' = ("where" #>> '{k8s_workload_name,_eq}')))
    AND ("where" #>> '{k8s_pod_name,_eq}' IS NULL OR (cr.tags ->> 'name' = ("where" #>> '{k8s_pod_name,_eq}')))
    AND ("where" #>> '{k8s_namespace_name,_eq}' IS NULL OR (cr.tags ->> 'namespace' = ("where" #>> '{k8s_namespace_name,_eq}')))
    AND ("where" #>> '{k8s_node_name,_eq}' IS NULL OR (cr.tags ->> 'node' = ("where" #>> '{k8s_node_name,_eq}')))
GROUP BY
    (CASE WHEN 'tenant_id' = ANY(group_by) THEN a.tenant END),
    (CASE WHEN 'account_id' = ANY(group_by) THEN cr.account END),
    (CASE WHEN 'resource_id' = ANY(group_by) THEN cr.id END),
    (CASE WHEN 'account_name' = ANY(group_by) THEN a.account_name END),
    (CASE WHEN 'metric' = ANY(group_by) THEN crm.metric END),
    (CASE WHEN 'name' = ANY(group_by) THEN cr.name END),
    (CASE WHEN 'k8s_workload_name' = ANY(group_by) THEN cr.tags ->> 'controller' END),
    (CASE WHEN 'k8s_pod_name' = ANY(group_by) THEN cr.tags ->> 'name' END),
    (CASE WHEN 'k8s_namespace_name' = ANY(group_by) THEN cr.tags ->> 'namespace' END),
    (CASE WHEN 'k8s_node_name' = ANY(group_by) THEN cr.tags ->> 'node' END)
$function$;

CREATE OR REPLACE FUNCTION public.cloud_resource_metrics_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, date_unit text DEFAULT 'day'::text, "limit" integer DEFAULT 100, "offset" integer DEFAULT 0, sort_by text DEFAULT 'timestamp'::text, sort_order text DEFAULT 'asc'::text, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF cloud_resource_metrics_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
     (CASE WHEN 'tenant_id' = ANY(group_by) THEN a.tenant ELSE null end ) AS tenant_id,
     (CASE WHEN 'account_name' = ANY(group_by) THEN a.account_name ELSE NULL END) AS account_name, 
     (CASE WHEN 'account_id' = ANY(group_by) THEN cr.account ELSE NULL END) AS account_id, 
     (CASE WHEN 'resource_id' = ANY(group_by) THEN cr.id ELSE null end ) AS resource_id,
     (CASE WHEN 'resource_name' = ANY(group_by) THEN cr.name ELSE null end ) AS resource_name,
     (CASE WHEN 'metric' = ANY(group_by) THEN crm.metric ELSE NULL END) AS metric,
     (CASE WHEN 'timestamp' = ANY(group_by) THEN date_trunc(date_unit, crm."timestamp") ELSE NULL END) AS "timestamp",
     COUNT(*) AS count_value,
     SUM(crm.value) AS sum_value,
     AVG(crm.value) AS avg_value,
     MIN(crm.value) AS min_value,
     MAX(crm.value) AS max_value
FROM cloud_resource_metrics crm
LEFT JOIN cloud_resourses cr ON crm.cloud_resource_id = cr.id 
LEFT JOIN cloud_accounts a ON a.id = cr.account
WHERE 
    ("hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL OR ( a.tenant = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid))
    AND ("where" #>> '{metric,_eq}' IS null OR (crm.metric = ("where" #>> '{metric,_eq}')))
    AND ("where" #>> '{metric,_in}' IS null OR (("where" #>> '{metric,_in}')::jsonb ? crm.metric::text))
    AND ("where" #>> '{account_id,_eq}' IS null OR (cr.account = ("where" #>> '{account_id,_eq}') :: uuid))
    AND ("where" #>> '{resource_name,_eq}' IS null OR (cr.name = ("where" #>> '{resource_name,_eq}')))
    AND ("where" #>> '{resource_id,_eq}' IS null OR (cr.id = ("where" #>> '{resource_id,_eq}')::uuid))
    AND ("where" #>> '{k8s_workload_name,_eq}' IS NULL OR (crm.tags ->> 'controller' = ("where" #>> '{k8s_workload_name,_eq}')))
    AND ("where" #>> '{k8s_pod_name,_eq}' IS NULL OR (crm.tags ->> 'name' = ("where" #>> '{k8s_pod_name,_eq}')))
    AND ("where" #>> '{k8s_namespace_name,_eq}' IS NULL OR (crm.tags ->> 'namespace' = ("where" #>> '{k8s_namespace_name,_eq}')))
    AND ("where" #>> '{k8s_node_name,_eq}' IS NULL OR (crm.tags ->> 'node' = ("where" #>> '{k8s_node_name,_eq}')))
    AND ("where" #>> '{timestamp,_lt}' IS NULL OR (crm."timestamp" < ("where" #>> '{timestamp,_lt}')::timestamp))
    AND ("where" #>> '{timestamp,_gt}' IS NULL OR (crm."timestamp" > ("where" #>> '{timestamp,_gt}')::timestamp))
    AND ("where" #>> '{timestamp,_le}' IS NULL OR (crm."timestamp" <= ("where" #>> '{timestamp,_lt}')::timestamp))
    AND ("where" #>> '{timestamp,_ge}' IS NULL OR (crm."timestamp" >= ("where" #>> '{timestamp,_lt}')::timestamp))
    AND ("where" #>> '{timestamp,_between}' IS NULL OR (("timestamp" >= ("where" #>> '{timestamp,_between,_ge}')::timestamp) and ("timestamp" <= ("where" #>> '{timestamp,_between,_le}')::timestamp)))
GROUP BY
    (CASE WHEN 'tenant_id' = ANY(group_by) THEN a.tenant END),
    (CASE WHEN 'account_id' = ANY(group_by) THEN cr.account END),
    (CASE WHEN 'account_name' = ANY(group_by) THEN a.account_name END),
    (CASE WHEN 'resource_name' = ANY(group_by) THEN cr.name END),
    (CASE WHEN 'resource_id' = ANY(group_by) THEN cr.id END),
    (CASE WHEN 'metric' = ANY(group_by) THEN crm.metric END),
    (CASE WHEN 'k8s_workload_name' = ANY(group_by) THEN crm.tags ->> 'controller' END),
    (CASE WHEN 'k8s_pod_name' = ANY(group_by) THEN crm.tags ->> 'name' END),
    (CASE WHEN 'k8s_namespace_name' = ANY(group_by) THEN crm.tags ->> 'namespace' END),
    (CASE WHEN 'k8s_node_name' = ANY(group_by) THEN crm.tags ->> 'node' END),
    (CASE WHEN 'timestamp' = ANY(group_by) THEN date_trunc(date_unit, crm.timestamp) END)
ORDER BY
    (case when sort_by = 'timestamp' and sort_order = 'asc' then max(date_trunc(date_unit, crm.timestamp)) end) asc,
    (case when sort_by = 'timestamp' and sort_order = 'desc' then max(date_trunc(date_unit, crm.timestamp)) end) desc
LIMIT "limit" OFFSET "offset";
$function$;

DROP FUNCTION "public"."metrics_groupings"("pg_catalog"."_text", "pg_catalog"."json", "pg_catalog"."json");

DROP VIEW "public"."cloud_resource_metrics_cloud_resourses_cloud_accounts";
