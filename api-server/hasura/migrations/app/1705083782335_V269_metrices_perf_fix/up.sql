CREATE INDEX cloud_resource_metrics_tenantaccounttimestamp ON cloud_resource_metrics (tenant_id, cloud_account_id, timestamp);



CREATE OR REPLACE FUNCTION public.k8s_pod_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, date_unit text DEFAULT 'day'::text, "limit" integer DEFAULT 100, "offset" integer DEFAULT 0, sort_by text DEFAULT 'timestamp'::text, sort_order text DEFAULT 'asc'::text, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF k8s_pod_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN crm.tenant_id
      ELSE NULL
    END
  ) AS tenant_id,
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN crm.cloud_account_id
      ELSE NULL
    END
  ) AS account_id,
  (
    CASE
      WHEN 'workload_type' = ANY(group_by) THEN crm.tags ->> 'controllerKind'
      ELSE NULL
    END
  ) AS workload_type,
  (
    CASE
      WHEN 'workload_name' = ANY(group_by) THEN crm.tags ->> 'controller'
      ELSE NULL
    END
  ) AS workload_name,
  (
    CASE
      WHEN 'namespace_name' = ANY(group_by) THEN crm.tags ->> 'namespace'
      ELSE NULL
    END
  ) AS namespace_name,
  (
    CASE
      WHEN 'pod_name' = ANY(group_by) THEN crm.tags ->> 'name'
      ELSE NULL
    END
  ) AS pod_name,
  (
    CASE
      WHEN 'node_name' = ANY(group_by) THEN crm.tags ->> 'node'
      ELSE NULL
    END
  ) AS node_name,
  (
    CASE
      WHEN 'timestamp' = ANY(group_by) THEN date_trunc(date_unit, crm."timestamp")
      ELSE NULL::timestamp
    END
  ) AS timestamp,
  sum(case when crm.metric = 'totalCost' then crm.value end) AS cost,
  avg(case when crm.metric = 'totalEfficiency' then crm.value end) AS avg_efficiency,
  max(case when crm.metric = 'totalEfficiency' then crm.value end) AS max_efficiency,
  avg(case when crm.metric = 'cpuCoreUsageAverage' then crm.value end) AS avg_cpu_used,
  max(case when crm.metric = 'cpuCoreUsageAverage' then crm.value end) AS max_cpu_used,
  avg(case when crm.metric = 'ramByteUsageAverage' then crm.value end) AS avg_memory_used,
  max(case when crm.metric = 'ramByteUsageAverage' then crm.value end) AS max_memory_used,
  avg(case when crm.metric = 'cpuCoreRequestAverage' then crm.value end) AS avg_cpu_request,
  max(case when crm.metric = 'cpuCoreRequestAverage' then crm.value end) AS max_cpu_request,
  avg(case when crm.metric = 'ramByteRequestAverage' then crm.value end) AS avg_memory_request,
  max(case when crm.metric = 'ramByteRequestAverage' then crm.value end) AS max_memory_request,
  avg(case when crm.metric = 'cpuEfficiency' then crm.value end) AS avg_cpu_efficiency,
  max(case when crm.metric = 'cpuEfficiency' then crm.value end) AS max_cpu_efficiency,
  avg(case when crm.metric = 'ramEfficiency' then crm.value end) AS avg_ram_efficiency,
  max(case when crm.metric = 'ramEfficiency' then crm.value end) AS max_ram_efficiency,
  sum(case when crm.metric = 'networkReceiveBytes' then crm.value end) AS sum_ingress,
  sum(case when crm.metric = 'networkTransferBytes' then crm.value end) AS sum_egress
from cloud_resource_metrics crm
where
  (
    "hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL
    OR (
      crm."tenant_id" = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid
    )
  )
  AND (
    "where" #>> '{account_id,_eq}' IS NULL
    OR (
      crm."cloud_account_id" = ("where" #>> '{account_id,_eq}') :: uuid
    )
  )
  AND crm.metric IN (
    'totalCost',
    'totalEfficiency',
    'cpuCoreUsageAverage',
    'ramByteUsageAverage',
    'cpuCoreRequestAverage',
    'ramByteRequestAverage',
    'cpuEfficiency',
    'ramEfficiency',
    'networkReceiveBytes',
    'networkTransferBytes'
  )
  AND (
    "where" #>> '{workload_type,_eq}' IS NULL
    OR (
      crm.tags ->> 'controllerKind' = ("where" #>> '{workload_type,_eq}')
    )
  )
  AND (
    "where" #>> '{workload_type,_in}' IS NULL
    OR (
      ("where" #>> '{workload_type,_in}')::jsonb ? (crm.tags ->> 'workload_type')::text
    )
  )
  AND (
    "where" #>> '{workload_name,_eq}' IS NULL
    OR (
      crm.tags ->> 'controller' = ("where" #>> '{workload_name,_eq}')
    )
  )
  AND (
    "where" #>> '{workload_fqdn,_eq}' IS NULL
    OR (
      crm.tags ->> 'workload_fqdn' = ("where" #>> '{workload_fqdn,_eq}')
    )
  )    
  AND (
    "where" #>> '{workload_fqdn,_in}' IS NULL
    OR (
      ("where" #>> '{workload_fqdn,_in}')::jsonb ? (crm.tags ->> 'workload_fqdn')::text
    )
  )
  AND (
    "where" #>> '{namespace_name,_eq}' IS NULL
    OR (
      crm.tags ->> 'namespace' = ("where" #>> '{namespace_name,_eq}')
    )
  )
  AND (
    "where" #>> '{namespace_name,_in}' IS NULL
    OR (
      ("where" #>> '{namespace_name,_in}')::jsonb ? (crm.tags ->> 'namespace')::text
    )
  )
  AND (
    "where" #>> '{pod_name,_eq}' IS NULL
    OR (
      crm.tags ->> 'name' = ("where" #>> '{pod_name,_eq}')
    )
  ) 
  AND (
    "where" #>> '{pod_fqdn,_eq}' IS NULL
    OR (
      crm.tags ->> 'pod_fqdn' = ("where" #>> '{pod_fqdn,_eq}')
    )
  )    
  AND (
    "where" #>> '{pod_fqdn,_in}' IS NULL
    OR (
      ("where" #>> '{pod_fqdn,_in}')::jsonb ? (crm.tags ->> 'pod_fqdn')::text
    )
  )
  AND (
    "where" #>> '{node_name,_eq}' IS NULL
    OR (
      crm.tags ->> 'node' = ("where" #>> '{node_name,_eq}')
    )
  )  
  AND (
    "where" #>> '{node_name,_in}' IS NULL
    OR (
      ("where" #>> '{node_name,_in}')::jsonb ? (crm.tags ->> 'node')::text
    )
  )
  AND (
    "where" #>> '{timestamp,_eq}' IS NULL
    OR (
      crm."timestamp" = ("where" #>> '{timestamp,_eq}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_gt}' IS NULL
    OR (
      crm."timestamp" > ("where" #>> '{timestamp,_gt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_lt}' IS NULL
    OR (
      crm."timestamp" < ("where" #>> '{timestamp,_lt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_le}' IS NULL
    OR (
      crm."timestamp" <= ("where" #>> '{timestamp,_le}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_ge}' IS NULL
    OR (
      crm."timestamp" >= ("where" #>> '{timestamp,_ge}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,le}' IS NULL
    OR (
      crm."timestamp" <= ("where" #>> '{timestamp,_between,le}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,lt}' IS NULL
    OR (
      crm."timestamp" < ("where" #>> '{timestamp,_between,lt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,gt}' IS NULL
    OR (
      crm."timestamp" > ("where" #>> '{timestamp,_between,gt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,ge}' IS NULL
    OR (
      crm."timestamp" >= ("where" #>> '{timestamp,_between,ge}') :: timestamp
    )
  )
group BY
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN crm.tenant_id
    END
  ),
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN crm.cloud_account_id
    END
  ),
  (
    CASE
      WHEN 'workload_type' = ANY(group_by) THEN crm.tags ->> 'controllerKind'
    END
  ),
  (
    CASE
      WHEN 'workload_name' = ANY(group_by) THEN crm.tags ->> 'controller'
    END
  ),
  (
    CASE
      WHEN 'namespace_name' = ANY(group_by) THEN crm.tags ->> 'namespace'
    END
  ),
  (
    CASE
      WHEN 'pod_name' = ANY(group_by) THEN crm.tags ->> 'name'
    END
  ),
  (
    CASE
      WHEN 'node_name' = ANY(group_by) THEN crm.tags ->> 'node'
    END
  ),
  (
    CASE
      WHEN 'timestamp' = ANY(group_by) THEN date_trunc(date_unit, crm."timestamp")
    END
  )
order by "timestamp" desc
limit "limit" offset "offset"  
$function$;
