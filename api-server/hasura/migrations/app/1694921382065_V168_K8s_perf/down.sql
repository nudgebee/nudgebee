
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE INDEX IF NOT EXISTS  cloud_resource_metrics_metric_resource_id ON public.cloud_resource_metrics USING btree (metric, cloud_resource_id);

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE index IF NOT EXISTS  cloud_resource_metrics_metric ON public.cloud_resource_metrics USING btree (metric);

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE INDEX IF NOT EXISTS cloud_resourse_type ON public.cloud_resourses USING btree (type);

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE OR REPLACE FUNCTION public.cloud_resource_k8s_pod_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, hasura_session json DEFAULT '{}'::json)
--  RETURNS SETOF cloud_resource_k8s_pod_groupings_type
--  LANGUAGE sql
--  STABLE
-- AS $function$
-- SELECT
--   (
--     CASE
--       WHEN 'account_id' = ANY(group_by) THEN account_id
--       ELSE NULL
--     END
--   ) AS account_id,
--   (
--     CASE
--       WHEN 'tenant_id' = ANY(group_by) THEN tenant_id
--       ELSE NULL
--     END
--   ) AS tenant_id,
--   (
--     CASE
--       WHEN 'workload_type' = ANY(group_by) THEN workload_type
--       ELSE NULL
--     END
--   ) AS workload_type,
--   (
--     CASE
--       WHEN 'workload_name' = ANY(group_by) THEN workload_name
--       ELSE NULL
--     END
--   ) AS workload_name,
--   (
--     CASE
--       WHEN 'namespace_name' = ANY(group_by) THEN namespace_name
--       ELSE NULL
--     END
--   ) AS namespace_name,
--   (
--     CASE
--       WHEN 'pod_name' = ANY(group_by) THEN pod_name
--       ELSE NULL
--     END
--   ) AS pod_name,
--   (
--     CASE
--       WHEN 'node_name' = ANY(group_by) THEN node_name
--       ELSE NULL
--     END
--   ) AS pod_name,
--   (
--     CASE
--       WHEN 'timestamp' = ANY(group_by) THEN "timestamp"
--       ELSE NULL::timestamp
--     END
--   ) AS timestamp,
--   sum(pod_cost) AS pod_cost,
--   avg(avg_cpu_used) AS avg_cpu_used,
--   max(max_cpu_used) AS max_cpu_used,
--   avg(avg_memory_used) AS avg_memory_used,
--   max(max_memory_used) AS max_memory_used,
--   avg(avg_cpu_request) AS avg_cpu_request,
--   max(max_cpu_request) AS max_cpu_request,
--   avg(avg_memory_request) AS avg_memory_request,
--   max(max_memory_request) AS max_memory_request,
--   avg(avg_cpu_efficiency) AS avg_cpu_efficiency,
--   max(max_cpu_efficiency) AS max_cpu_efficiency,
--   avg(avg_ram_efficiency) AS avg_ram_efficiency,
--   max(max_ram_efficiency) AS max_ram_efficiency
-- from
--   cloudaccount_k8s_pod_aggregate
-- where
--   (
--     "hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL
--     OR (
--       "tenant_id" = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid
--     )
--   )
--   AND (
--     "where" #>> '{account_id,_eq}' IS NULL
--     OR (
--       "account_id" = ("where" #>> '{account_id,_eq}') :: uuid
--     )
--   )
--   AND (
--     "where" #>> '{workload_type,_eq}' IS NULL
--     OR (
--       "workload_type" = ("where" #>> '{workload_type,_eq}')
--     )
--   )
--   AND (
--     "where" #>> '{workload_name,_eq}' IS NULL
--     OR (
--       "workload_name" = ("where" #>> '{workload_name,_eq}')
--     )
--   )
--   AND (
--     "where" #>> '{namespace_name,_eq}' IS NULL
--     OR (
--       "namespace_name" = ("where" #>> '{namespace_name,_eq}')
--     )
--   )
--   AND (
--     "where" #>> '{pod_name,_eq}' IS NULL
--     OR (
--       "pod_name" = ("where" #>> '{pod_name,_eq}')
--     )
--   )
--   AND (
--     "where" #>> '{node_name,_eq}' IS NULL
--     OR (
--       "node_name" = ("where" #>> '{node_name,_eq}')
--     )
--   )
--   AND (
--     "where" #>> '{timestamp,_eq}' IS NULL
--     OR (
--       "timestamp" = ("where" #>> '{timestamp,_eq}') :: timestamp
--     )
--   )
--   AND (
--     "where" #>> '{timestamp,_gt}' IS NULL
--     OR (
--       "timestamp" > ("where" #>> '{timestamp,_eq}') :: timestamp
--     )
--   )
--   AND (
--     "where" #>> '{timestamp,_lt}' IS NULL
--     OR (
--       "timestamp" < ("where" #>> '{timestamp,_eq}') :: timestamp
--     )
--   )
--   AND (
--     "where" #>> '{timestamp,_le}' IS NULL
--     OR (
--       "timestamp" <= ("where" #>> '{timestamp,_eq}') :: timestamp
--     )
--   )
--   AND (
--     "where" #>> '{timestamp,_ge}' IS NULL
--     OR (
--       "timestamp" >= ("where" #>> '{timestamp,_eq}') :: timestamp
--     )
--   )
--   AND (
--     "where" #>> '{timestamp,_between,le}' IS NULL
--     OR (
--       "timestamp" <= ("where" #>> '{timestamp,_between,le}') :: timestamp
--     )
--   )
--   AND (
--     "where" #>> '{timestamp,_between,lt}' IS NULL
--     OR (
--       "timestamp" < ("where" #>> '{timestamp,_between,lt}') :: timestamp
--     )
--   )
--   AND (
--     "where" #>> '{timestamp,_between,gt}' IS NULL
--     OR (
--       "timestamp" > ("where" #>> '{timestamp,_between,gt}') :: timestamp
--     )
--   )
--   AND (
--     "where" #>> '{timestamp,_between,ge}' IS NULL
--     OR (
--       "timestamp" >= ("where" #>> '{timestamp,_between,ge}') :: timestamp
--     )
--   )
-- group BY
--   (
--     CASE
--       WHEN 'tenant_id' = ANY(group_by) THEN tenant_id
--     END
--   ),
--   (
--     CASE
--       WHEN 'account_id' = ANY(group_by) THEN account_id
--     END
--   ),
--   (
--     CASE
--       WHEN 'workload_type' = ANY(group_by) THEN workload_type
--     END
--   ),
--   (
--     CASE
--       WHEN 'workload_name' = ANY(group_by) THEN workload_name
--     END
--   ),
--   (
--     CASE
--       WHEN 'namespace_name' = ANY(group_by) THEN namespace_name
--     END
--   ),
--   (
--     CASE
--       WHEN 'pod_name' = ANY(group_by) THEN pod_name
--     END
--   ),
--   (
--     CASE
--       WHEN 'node_name' = ANY(group_by) THEN node_name
--     END
--   ),
--   (
--     CASE
--       WHEN 'timestamp' = ANY(group_by) THEN "timestamp"
--     END
--   )
--   $function$;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE
-- OR REPLACE VIEW "public"."cloud_resource_k8s_pod_groupings_type" AS
-- SELECT
--   cloudaccount_k8s_pod_aggregate.account_id AS account_id,
--   cloudaccount_k8s_pod_aggregate.tenant_id AS tenant_id,
--   cloudaccount_k8s_pod_aggregate.workload_type,
--   cloudaccount_k8s_pod_aggregate.workload_name,
--   cloudaccount_k8s_pod_aggregate.namespace_name,
--   cloudaccount_k8s_pod_aggregate.pod_name,
--   cloudaccount_k8s_pod_aggregate.node_name,
--   cloudaccount_k8s_pod_aggregate."timestamp",
--   sum(cloudaccount_k8s_pod_aggregate.pod_cost) AS pod_cost,
--   avg(cloudaccount_k8s_pod_aggregate.avg_cpu_used) AS avg_cpu_used,
--   max(cloudaccount_k8s_pod_aggregate.max_cpu_used) AS max_cpu_used,
--   avg(cloudaccount_k8s_pod_aggregate.avg_memory_used) AS avg_memory_used,
--   max(cloudaccount_k8s_pod_aggregate.max_memory_used) AS max_memory_used,
--   avg(cloudaccount_k8s_pod_aggregate.avg_cpu_request) AS avg_cpu_request,
--   max(cloudaccount_k8s_pod_aggregate.max_cpu_request) AS max_cpu_request,
--   avg(
--     cloudaccount_k8s_pod_aggregate.avg_memory_request
--   ) AS avg_memory_request,
--   max(
--     cloudaccount_k8s_pod_aggregate.max_memory_request
--   ) AS max_memory_request,
--   avg(
--     cloudaccount_k8s_pod_aggregate.avg_cpu_efficiency
--   ) AS avg_cpu_efficiency,
--   max(
--     cloudaccount_k8s_pod_aggregate.max_cpu_efficiency
--   ) AS max_cpu_efficiency,
--   avg(
--     cloudaccount_k8s_pod_aggregate.avg_ram_efficiency
--   ) AS avg_ram_efficiency,
--   max(
--     cloudaccount_k8s_pod_aggregate.max_ram_efficiency
--   ) AS max_ram_efficiency
-- FROM
--   cloudaccount_k8s_pod_aggregate
-- WHERE
--   false
-- GROUP BY
--   cloudaccount_k8s_pod_aggregate.account_id,
--   cloudaccount_k8s_pod_aggregate.tenant_id,
--   cloudaccount_k8s_pod_aggregate.workload_type,
--   cloudaccount_k8s_pod_aggregate.workload_name,
--   cloudaccount_k8s_pod_aggregate.namespace_name,
--   cloudaccount_k8s_pod_aggregate.pod_name,
--   cloudaccount_k8s_pod_aggregate.node_name,
--   cloudaccount_k8s_pod_aggregate."timestamp";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_pod_aggregate" AS
-- SELECT
--   ca.tenant as tenant_id,
--   ca.id as account_id,
--   (cr.tags ->> 'controllerKind' :: text) AS workload_type,
--   (cr.tags ->> 'controller' :: text) AS workload_name,
--   (cr.tags ->> 'namespace' :: text) AS namespace_name,
--   (cr.tags ->> 'pod' :: text) AS pod_name,
--   (cr.tags ->> 'node' :: text) AS node_name,
--   cr.is_active,
--   s.date AS "timestamp",
--   sum(s.amount) AS pod_cost,
--   avg(
--     CASE
--       WHEN (crm.metric = 'cpuCoreUsageAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_cpu_used,
--   max(
--     CASE
--       WHEN (crm.metric = 'cpuCoreUsageAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_cpu_used,
--   avg(
--     CASE
--       WHEN (crm.metric = 'ramByteUsageAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_memory_used,
--   max(
--     CASE
--       WHEN (crm.metric = 'ramByteUsageAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_memory_used,
--   avg(
--     CASE
--       WHEN (crm.metric = 'cpuCoreRequestAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_cpu_request,
--   max(
--     CASE
--       WHEN (crm.metric = 'cpuCoreRequestAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_cpu_request,
--   avg(
--     CASE
--       WHEN (crm.metric = 'ramByteRequestAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_memory_request,
--   max(
--     CASE
--       WHEN (crm.metric = 'ramByteRequestAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_memory_request,
--   avg(
--     CASE
--       WHEN (crm.metric = 'cpuEfficiency' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_cpu_efficiency,
--   max(
--     CASE
--       WHEN (crm.metric = 'cpuEfficiency' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_cpu_efficiency,
--   avg(
--     CASE
--       WHEN (crm.metric = 'ramEfficiency' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_ram_efficiency,
--   max(
--     CASE
--       WHEN (crm.metric = 'ramEfficiency' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_ram_efficiency,
--   count(*) AS container_count
-- FROM
--   (
--     (
--       (
--         cloud_accounts ca
--         JOIN cloud_resourses cr ON ((cr.account = ca.id))
--       )
--       JOIN spends s ON (
--         (
--           (s.cloud_account = ca.id)
--           AND (s.cloud_resource_id = cr.id)
--         )
--       )
--     )
--     JOIN cloud_resource_metrics crm ON ((crm.cloud_resource_id = cr.id))
--   )
-- WHERE
--   (
--     (ca.account_type = 'kubernetes' :: text)
--     AND (lower(cr.type) = 'pod' :: text)
--     AND (cr.is_active IS NOT FALSE)
--     AND ((cr.tags ->> 'controllerKind' :: text) IS NOT NULL)
--     AND (
--       crm.metric = ANY (
--         ARRAY ['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text]
--       )
--     )
--   )
-- GROUP BY
--   ca.id,
--   ca.tenant,
--   (cr.tags ->> 'controllerKind' :: text),
--   (cr.tags ->> 'controller' :: text),
--   (cr.tags ->> 'namespace' :: text),
--   (cr.tags ->> 'pod' :: text),
--   (cr.tags ->> 'node' :: text),
--   cr.is_active,
--   s.date;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."cloudaccount_k8s_pod_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."cloud_resource_k8s_pod_groupings_type";

CREATE OR REPLACE FUNCTION public.cloud_resource_k8s_pod_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF cloud_resource_k8s_pod_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
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

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_metrics_aggregate" AS
-- SELECT
--   cr.tenant as tenant_id,
--   cr.account as account_id,
--   crm."timestamp",
--   crm.metric,
--   sum(crm.value) AS sum_value,
--   avg(crm.value) AS avg_value,
--   max(crm.value) AS max_value,
--   min(crm.value) AS min_value,
--   count(crm.value) AS count_value
-- FROM
--   (
--     cloud_resource_metrics crm
--     JOIN cloud_resourses cr ON ((cr.id = crm.cloud_resource_id))
--   )
-- WHERE
--   (
--     (lower(cr.type) = 'pod' :: text)
--     AND (cr.is_active IS NOT FALSE)
--     AND (
--       cr.account IN (
--         SELECT
--           cloud_accounts.id
--         FROM
--           cloud_accounts
--         WHERE
--           (cloud_accounts.account_type = 'kubernetes' :: text)
--       )
--     )
--   )
-- GROUP BY
--   cr.tenant,
--   cr.account,
--   crm."timestamp",
--   crm.metric;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."cloudaccount_k8s_metrics_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_aggregate" AS
-- SELECT
--   ca.tenant as tenant_id,
--   ca.id as account_id,
--   ca.account_name,
--   count(
--     DISTINCT CASE
--       WHEN ((cr.tags ->> 'controllerKind' :: text) IS NOT NULL) THEN (cr.tags ->> 'controller' :: text)
--       ELSE NULL :: text
--     END
--   ) AS count_workloads,
--   count(
--     DISTINCT CASE
--       WHEN ((cr.tags ->> 'node' :: text) IS NOT NULL) THEN (cr.tags ->> 'node' :: text)
--       ELSE NULL :: text
--     END
--   ) AS count_hosts,
--   count(
--     DISTINCT CASE
--       WHEN ((cr.tags ->> 'pod' :: text) IS NOT NULL) THEN (cr.tags ->> 'pod' :: text)
--       ELSE NULL :: text
--     END
--   ) AS count_pods
-- FROM
--   (
--     cloud_accounts ca
--     JOIN cloud_resourses cr ON ((cr.account = ca.id))
--   )
-- WHERE
--   (
--     (ca.account_type = 'kubernetes' :: text)
--     AND (lower(cr.type) = 'pod' :: text)
--     AND (cr.is_active IS NOT FALSE)
--   )
-- GROUP BY
--   ca.id,
--   ca.account_name,
--   ca.tenant;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."cloudaccount_k8s_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_workload_aggregate" AS
-- SELECT
--   ca.tenant as tenant_id,
--   ca.id as account_id,
--   (cr.tags ->> 'namespace' :: text) AS namespace_name,
--   (cr.tags ->> 'controllerKind' :: text) AS workload_type,
--   (cr.tags ->> 'controller' :: text) AS workload_name,
--   s.date AS "timestamp",
--   sum(s.amount) AS workload_cost,
--   avg(
--     CASE
--       WHEN (crm.metric = 'cpuCoreUsageAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_cpu_used,
--   max(
--     CASE
--       WHEN (crm.metric = 'cpuCoreUsageAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_cpu_used,
--   avg(
--     CASE
--       WHEN (crm.metric = 'ramByteUsageAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_memory_used,
--   max(
--     CASE
--       WHEN (crm.metric = 'ramByteUsageAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_memory_used,
--   avg(
--     CASE
--       WHEN (crm.metric = 'cpuCoreRequestAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_cpu_request,
--   max(
--     CASE
--       WHEN (crm.metric = 'cpuCoreRequestAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_cpu_request,
--   avg(
--     CASE
--       WHEN (crm.metric = 'ramByteRequestAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_memory_request,
--   max(
--     CASE
--       WHEN (crm.metric = 'ramByteRequestAverage' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_memory_request,
--   avg(
--     CASE
--       WHEN (crm.metric = 'cpuEfficiency' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_cpu_efficiency,
--   max(
--     CASE
--       WHEN (crm.metric = 'cpuEfficiency' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_cpu_efficiency,
--   avg(
--     CASE
--       WHEN (crm.metric = 'ramEfficiency' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS avg_ram_efficiency,
--   max(
--     CASE
--       WHEN (crm.metric = 'ramEfficiency' :: text) THEN crm.value
--       ELSE NULL :: double precision
--     END
--   ) AS max_ram_efficiency,
--   count(
--     DISTINCT CASE
--       WHEN ((cr.tags ->> 'pod' :: text) IS NOT NULL) THEN (cr.tags ->> 'pod' :: text)
--       ELSE NULL :: text
--     END
--   ) AS pod_count
-- FROM
--   (
--     (
--       (
--         cloud_accounts ca
--         JOIN cloud_resourses cr ON ((cr.account = ca.id))
--       )
--       JOIN spends s ON (
--         (
--           (s.cloud_account = ca.id)
--           AND (s.cloud_resource_id = cr.id)
--         )
--       )
--     )
--     JOIN cloud_resource_metrics crm ON ((crm.cloud_resource_id = cr.id))
--   )
-- WHERE
--   (
--     (ca.account_type = 'kubernetes' :: text)
--     AND (lower(cr.type) = 'pod' :: text)
--     AND (cr.is_active IS NOT FALSE)
--     AND ((cr.tags ->> 'controllerKind' :: text) IS NOT NULL)
--     AND (
--       crm.metric = ANY (
--         ARRAY ['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text]
--       )
--     )
--   )
-- GROUP BY
--   ca.id,
--   ca.tenant,
--   (cr.tags ->> 'controllerKind' :: text),
--   (cr.tags ->> 'controller' :: text),
--   (cr.tags ->> 'namespace' :: text),
--   s.date;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."cloudaccount_k8s_workload_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_node_aggregate" AS
-- SELECT cr.tenant as tenant_id,
--     cr.account as account_id,
--     cr.tags ->> 'node'::text AS node_name,
--     s.date AS "timestamp",
--     case when (intnc.meta ->> 'spotted'::text = 'true') then 'spot' else 'on_demand' end AS node_type,
--     intnc.meta ->> 'flavor'::text AS node_flavor,
--     sum(s.amount) AS workload_cost,
--     avg(
--         CASE
--             WHEN crm.metric = 'cpuCoreUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_cpu_used,
--     max(
--         CASE
--             WHEN crm.metric = 'cpuCoreUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_cpu_used,
--     avg(
--         CASE
--             WHEN crm.metric = 'ramByteUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_memory_used,
--     max(
--         CASE
--             WHEN crm.metric = 'ramByteUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_memory_used,
--     avg(
--         CASE
--             WHEN crm.metric = 'cpuCoreRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_cpu_request,
--     max(
--         CASE
--             WHEN crm.metric = 'cpuCoreRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_cpu_request,
--     avg(
--         CASE
--             WHEN crm.metric = 'ramByteRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_memory_request,
--     max(
--         CASE
--             WHEN crm.metric = 'ramByteRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_memory_request,
--     avg(
--         CASE
--             WHEN crm.metric = 'cpuEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_cpu_efficiency,
--     max(
--         CASE
--             WHEN crm.metric = 'cpuEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_cpu_efficiency,
--     avg(
--         CASE
--             WHEN crm.metric = 'ramEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_ram_efficiency,
--     max(
--         CASE
--             WHEN crm.metric = 'ramEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_ram_efficiency,
--     count(DISTINCT
--         CASE
--             WHEN (cr.tags ->> 'pod'::text) IS NOT NULL THEN cr.tags ->> 'pod'::text
--             ELSE NULL::text
--         END) AS pod_count
--   FROM cloud_resourses cr
--   JOIN spends s ON s.cloud_account = cr.account AND s.cloud_resource_id = cr.id
--   JOIN cloud_resource_metrics crm ON crm.cloud_resource_id = cr.id and crm.metric in ('cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text)
--   LEFT JOIN cloud_resourses intnc ON intnc.tenant = cr.tenant AND intnc.service_name = 'AmazonEC2'::text AND (cr.tags ->> 'node'::text) = (intnc.meta ->> 'private_dns_name'::text)
--   WHERE lower(cr.type) = 'pod'::text AND cr.is_active IS NOT FALSE AND (cr.tags ->> 'node'::text) IS NOT null
--   GROUP BY cr.account, cr.tenant, (cr.tags ->> 'node'::text), (intnc.meta ->> 'spotted'::text), (intnc.meta ->> 'flavor'::text), s.date;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."cloudaccount_k8s_node_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE OR REPLACE VIEW "public"."cloudaccount_k8s_node_aggregate" AS
-- SELECT cr.account as id,
--     cr.tenant,
--     cr.tags ->> 'node'::text AS node_name,
--     s.date AS "timestamp",
--     sum(s.amount) AS workload_cost,
--     avg(
--         CASE
--             WHEN crm.metric = 'cpuCoreUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_cpu_used,
--     max(
--         CASE
--             WHEN crm.metric = 'cpuCoreUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_cpu_used,
--     avg(
--         CASE
--             WHEN crm.metric = 'ramByteUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_memory_used,
--     max(
--         CASE
--             WHEN crm.metric = 'ramByteUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_memory_used,
--     avg(
--         CASE
--             WHEN crm.metric = 'cpuCoreRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_cpu_request,
--     max(
--         CASE
--             WHEN crm.metric = 'cpuCoreRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_cpu_request,
--     avg(
--         CASE
--             WHEN crm.metric = 'ramByteRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_memory_request,
--     max(
--         CASE
--             WHEN crm.metric = 'ramByteRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_memory_request,
--     avg(
--         CASE
--             WHEN crm.metric = 'cpuEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_cpu_efficiency,
--     max(
--         CASE
--             WHEN crm.metric = 'cpuEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_cpu_efficiency,
--     avg(
--         CASE
--             WHEN crm.metric = 'ramEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_ram_efficiency,
--     max(
--         CASE
--             WHEN crm.metric = 'ramEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_ram_efficiency,
--     count(DISTINCT
--         CASE
--             WHEN (cr.tags ->> 'pod'::text) IS NOT NULL THEN cr.tags ->> 'pod'::text
--             ELSE NULL::text
--         END) AS pod_count,
--     intnc.meta ->> 'spotted'::text AS node_is_spot,
--     intnc.meta ->> 'flavor'::text AS node_flavor
--   FROM cloud_resourses cr
--   JOIN spends s ON s.cloud_account = cr.account AND s.cloud_resource_id = cr.id
--   JOIN cloud_resource_metrics crm ON crm.cloud_resource_id = cr.id and crm.metric in ('cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text)
--   LEFT JOIN cloud_resourses intnc ON intnc.tenant = cr.tenant AND intnc.service_name = 'AmazonEC2'::text AND (cr.tags ->> 'node'::text) = (intnc.meta ->> 'private_dns_name'::text)
--   WHERE lower(cr.type) = 'pod'::text AND cr.is_active IS NOT FALSE AND (cr.tags ->> 'node'::text) IS NOT null
--   GROUP BY cr.account, cr.tenant, (cr.tags ->> 'node'::text), (intnc.meta ->> 'spotted'::text), (intnc.meta ->> 'flavor'::text), s.date;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE OR REPLACE VIEW "public"."cloudaccount_k8s_node_aggregate" AS
-- SELECT cr.account as id,
--     cr.tenant,
--     cr.tags ->> 'node'::text AS node_name,
--     s.date AS "timestamp",
--     sum(s.amount) AS workload_cost,
--     avg(
--         CASE
--             WHEN crm.metric = 'cpuCoreUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_cpu_used,
--     max(
--         CASE
--             WHEN crm.metric = 'cpuCoreUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_cpu_used,
--     avg(
--         CASE
--             WHEN crm.metric = 'ramByteUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_memory_used,
--     max(
--         CASE
--             WHEN crm.metric = 'ramByteUsageAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_memory_used,
--     avg(
--         CASE
--             WHEN crm.metric = 'cpuCoreRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_cpu_request,
--     max(
--         CASE
--             WHEN crm.metric = 'cpuCoreRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_cpu_request,
--     avg(
--         CASE
--             WHEN crm.metric = 'ramByteRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_memory_request,
--     max(
--         CASE
--             WHEN crm.metric = 'ramByteRequestAverage'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_memory_request,
--     avg(
--         CASE
--             WHEN crm.metric = 'cpuEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_cpu_efficiency,
--     max(
--         CASE
--             WHEN crm.metric = 'cpuEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_cpu_efficiency,
--     avg(
--         CASE
--             WHEN crm.metric = 'ramEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS avg_ram_efficiency,
--     max(
--         CASE
--             WHEN crm.metric = 'ramEfficiency'::text THEN crm.value
--             ELSE NULL::double precision
--         END) AS max_ram_efficiency,
--     count(DISTINCT
--         CASE
--             WHEN (cr.tags ->> 'pod'::text) IS NOT NULL THEN cr.tags ->> 'pod'::text
--             ELSE NULL::text
--         END) AS pod_count,
--     intnc.meta ->> 'spotted'::text AS node_is_spot,
--     intnc.meta ->> 'flavor'::text AS node_flavor
--   FROM cloud_resourses cr
--   JOIN spends s ON s.cloud_account = cr.account AND s.cloud_resource_id = cr.id
--   JOIN cloud_resource_metrics crm ON crm.cloud_resource_id = cr.id and crm.metric in ('cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text)
--   LEFT JOIN cloud_resourses intnc ON intnc.tenant = cr.tenant AND intnc.service_name = 'AmazonEC2'::text AND (cr.tags ->> 'node'::text) = (intnc.meta ->> 'private_dns_name'::text)
--   WHERE lower(cr.type) = 'pod'::text AND cr.is_active IS NOT FALSE AND (cr.tags ->> 'node'::text) IS NOT null
--   GROUP BY cr.account, cr.tenant, (cr.tags ->> 'node'::text), (intnc.meta ->> 'spotted'::text), (intnc.meta ->> 'flavor'::text), s.date;
