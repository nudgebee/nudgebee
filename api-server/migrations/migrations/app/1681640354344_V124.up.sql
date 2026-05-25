
CREATE
OR REPLACE VIEW "public"."cloud_resource_k8s_pod_groupings_type" AS
SELECT
  cloudaccount_k8s_pod_aggregate.id AS account_id,
  cloudaccount_k8s_pod_aggregate.tenant AS tenant_id,
  cloudaccount_k8s_pod_aggregate.workload_type AS workload_type,
  cloudaccount_k8s_pod_aggregate.workload_name AS workload_name,
  cloudaccount_k8s_pod_aggregate.namespace_name AS namespace_name,
  cloudaccount_k8s_pod_aggregate.pod_name AS pod_name,
  cloudaccount_k8s_pod_aggregate.node_name AS node_name,
  cloudaccount_k8s_pod_aggregate.timestamp AS timestamp,
  sum(cloudaccount_k8s_pod_aggregate.pod_cost) AS pod_cost,
  avg(cloudaccount_k8s_pod_aggregate.avg_cpu_used) AS avg_cpu_used,
  max(cloudaccount_k8s_pod_aggregate.max_cpu_used) AS max_cpu_used,
  avg(cloudaccount_k8s_pod_aggregate.avg_memory_used) AS avg_memory_used,
  max(cloudaccount_k8s_pod_aggregate.max_memory_used) AS max_memory_used,
  avg(cloudaccount_k8s_pod_aggregate.avg_cpu_request) AS avg_cpu_request,
  max(cloudaccount_k8s_pod_aggregate.max_cpu_request) AS max_cpu_request,
  avg(cloudaccount_k8s_pod_aggregate.avg_memory_request) AS avg_memory_request,
  max(cloudaccount_k8s_pod_aggregate.max_memory_request) AS max_memory_request,
  avg(cloudaccount_k8s_pod_aggregate.avg_cpu_efficiency) AS avg_cpu_efficiency,
  max(cloudaccount_k8s_pod_aggregate.max_cpu_efficiency) AS max_cpu_efficiency,
  avg(cloudaccount_k8s_pod_aggregate.avg_ram_efficiency) AS avg_ram_efficiency,
  max(cloudaccount_k8s_pod_aggregate.max_ram_efficiency) AS max_ram_efficiency
FROM
  cloudaccount_k8s_pod_aggregate
WHERE
  false
GROUP BY
  cloudaccount_k8s_pod_aggregate.id,
  cloudaccount_k8s_pod_aggregate.tenant,
  cloudaccount_k8s_pod_aggregate.workload_type,
  cloudaccount_k8s_pod_aggregate.workload_name,
  cloudaccount_k8s_pod_aggregate.namespace_name,
  cloudaccount_k8s_pod_aggregate.pod_name,
  cloudaccount_k8s_pod_aggregate.node_name,
  cloudaccount_k8s_pod_aggregate.timestamp
;

CREATE
OR REPLACE FUNCTION public.cloud_resource_k8s_pod_groupings(
  group_by text [] DEFAULT '{}' :: text [],
  "where" json DEFAULT NULL :: json,
  hasura_session json DEFAULT '{}' :: json
) RETURNS SETOF cloud_resource_k8s_pod_groupings_type LANGUAGE sql STABLE AS $function$
SELECT
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN id
      ELSE NULL
    END
  ) AS account_id,
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN tenant
      ELSE NULL
    END
  ) AS tenant_id,
  (
    CASE
      WHEN 'workload_type' = ANY(group_by) THEN workload_type
      ELSE NULL
    END
  ) AS workload_type,
  (
    CASE
      WHEN 'workload_name' = ANY(group_by) THEN workload_name
      ELSE NULL
    END
  ) AS workload_name,
  (
    CASE
      WHEN 'namespace_name' = ANY(group_by) THEN namespace_name
      ELSE NULL
    END
  ) AS namespace_name,
  (
    CASE
      WHEN 'pod_name' = ANY(group_by) THEN pod_name
      ELSE NULL
    END
  ) AS pod_name,
  (
    CASE
      WHEN 'node_name' = ANY(group_by) THEN node_name
      ELSE NULL
    END
  ) AS pod_name,
  (
    CASE
      WHEN 'timestamp' = ANY(group_by) THEN "timestamp"
      ELSE NULL::timestamp
    END
  ) AS timestamp,
  sum(pod_cost) AS pod_cost,
  avg(avg_cpu_used) AS avg_cpu_used,
  max(max_cpu_used) AS max_cpu_used,
  avg(avg_memory_used) AS avg_memory_used,
  max(max_memory_used) AS max_memory_used,
  avg(avg_cpu_request) AS avg_cpu_request,
  max(max_cpu_request) AS max_cpu_request,
  avg(avg_memory_request) AS avg_memory_request,
  max(max_memory_request) AS max_memory_request,
  avg(avg_cpu_efficiency) AS avg_cpu_efficiency,
  max(max_cpu_efficiency) AS max_cpu_efficiency,
  avg(avg_ram_efficiency) AS avg_ram_efficiency,
  max(max_ram_efficiency) AS max_ram_efficiency
from
  cloudaccount_k8s_pod_aggregate
where
  (
    "hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL
    OR (
      "tenant" = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid
    )
  )
  AND (
    "where" #>> '{account_id,_eq}' IS NULL
    OR (
      "id" = ("where" #>> '{account_id,_eq}') :: uuid
    )
  )
  AND (
    "where" #>> '{workload_type,_eq}' IS NULL
    OR (
      "workload_type" = ("where" #>> '{workload_type,_eq}')
    )
  )
  AND (
    "where" #>> '{workload_name,_eq}' IS NULL
    OR (
      "workload_name" = ("where" #>> '{workload_name,_eq}')
    )
  )
  AND (
    "where" #>> '{namespace_name,_eq}' IS NULL
    OR (
      "namespace_name" = ("where" #>> '{namespace_name,_eq}')
    )
  )
  AND (
    "where" #>> '{pod_name,_eq}' IS NULL
    OR (
      "pod_name" = ("where" #>> '{pod_name,_eq}')
    )
  )  
  AND (
    "where" #>> '{node_name,_eq}' IS NULL
    OR (
      "node_name" = ("where" #>> '{node_name,_eq}')
    )
  )  
  AND (
    "where" #>> '{timestamp,_eq}' IS NULL
    OR (
      "timestamp" = ("where" #>> '{timestamp,_eq}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_gt}' IS NULL
    OR (
      "timestamp" > ("where" #>> '{timestamp,_eq}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_lt}' IS NULL
    OR (
      "timestamp" < ("where" #>> '{timestamp,_eq}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_le}' IS NULL
    OR (
      "timestamp" <= ("where" #>> '{timestamp,_eq}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_ge}' IS NULL
    OR (
      "timestamp" >= ("where" #>> '{timestamp,_eq}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,le}' IS NULL
    OR (
      "timestamp" <= ("where" #>> '{timestamp,_between,le}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,lt}' IS NULL
    OR (
      "timestamp" < ("where" #>> '{timestamp,_between,lt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,gt}' IS NULL
    OR (
      "timestamp" > ("where" #>> '{timestamp,_between,gt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,ge}' IS NULL
    OR (
      "timestamp" >= ("where" #>> '{timestamp,_between,ge}') :: timestamp
    )
  )
group BY
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN tenant
    END
  ),
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN id
    END
  ),
  (
    CASE
      WHEN 'workload_type' = ANY(group_by) THEN workload_type
    END
  ),
  (
    CASE
      WHEN 'workload_name' = ANY(group_by) THEN workload_name
    END
  ),
  (
    CASE
      WHEN 'namespace_name' = ANY(group_by) THEN namespace_name
    END
  ),
  (
    CASE
      WHEN 'pod_name' = ANY(group_by) THEN pod_name
    END
  ),
  (
    CASE
      WHEN 'node_name' = ANY(group_by) THEN node_name
    END
  ),
  (
    CASE
      WHEN 'timestamp' = ANY(group_by) THEN "timestamp"
    END
  )
  $function$;
