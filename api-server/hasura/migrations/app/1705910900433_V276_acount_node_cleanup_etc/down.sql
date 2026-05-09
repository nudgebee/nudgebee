
alter table "public"."k8s_pods" drop constraint "k8s_pods_cloud_account_id_fkey";

alter table "public"."k8s_nodes" drop constraint "k8s_nodes_cloud_account_id_fkey";


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- drop view if exists cloudaccount_k8s_resource_node_aggregate;


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_node_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_pod_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_score_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_namespace_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_workload_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_security_recommendation";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_namespace_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_workload_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_aggregate";

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
      WHEN 'account_id' = ANY(group_by) THEN account_id
      ELSE NULL
    END
  ) AS account_id,
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN tenant_id
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
  cloudaccount_k8s_resource_pod_aggregate
where
  (
    "hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL
    OR (
      "tenant_id" = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid
    )
  )
  AND (
    "where" #>> '{account_id,_eq}' IS NULL
    OR (
      "account_id" = ("where" #>> '{account_id,_eq}') :: uuid
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
      "timestamp" > ("where" #>> '{timestamp,_gt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_lt}' IS NULL
    OR (
      "timestamp" < ("where" #>> '{timestamp,_lt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_le}' IS NULL
    OR (
      "timestamp" <= ("where" #>> '{timestamp,_le}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_ge}' IS NULL
    OR (
      "timestamp" >= ("where" #>> '{timestamp,_ge}') :: timestamp
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
      WHEN 'tenant_id' = ANY(group_by) THEN tenant_id
    END
  ),
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN account_id
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
order by "timestamp" desc
  
  $function$;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."cloud_account_service_groupings_type";

CREATE OR REPLACE FUNCTION public.cloud_account_service_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF cloud_account_service_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN tenant
      ELSE NULL
    END
  ) AS tenant_id,
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN account
      ELSE NULL
    END
  ) AS account_id,
  (
    CASE
      WHEN 'service_name' = ANY(group_by) THEN service_name
      ELSE NULL
    END
  ) AS service_name,
  (
    CASE
      WHEN 'region' = ANY(group_by) THEN region
      ELSE NULL
    END
  ) AS region,
  (
    CASE
      WHEN 'spend_date' = ANY(group_by) THEN spend_date
      ELSE NULL
    END
  ) AS spend_date,
  sum(resource_count) as resource_count,
  sum(spend_amount) as spend_amount,
  sum(resource_estimated_saving) as resource_estimated_saving,
  (
    CASE
      WHEN 'account_name' = ANY(group_by) THEN account_name
      ELSE NULL
    END
  ) AS account_name,
  (
    CASE
      WHEN 'account_cloud_provider' = ANY(group_by) THEN account_cloud_provider
      ELSE NULL
    END
  ) AS account_cloud_provider
from
  cloud_services_aggregate
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
      "account" = ("where" #>> '{account_id,_eq}') :: uuid
    )
  )
  AND (
    "where" #>> '{account_name,_eq}' IS NULL
    OR (
      "account_name" = ("where" #>> '{account_name,_eq}')
    )
  )
  AND (
    "where" #>> '{account_cloud_provider,_eq}' IS NULL
    OR (
      "account_cloud_provider" = ("where" #>> '{account_cloud_provider,_eq}')
    )
  )
  AND (
    "where" #>> '{region,_eq}' IS NULL
    OR (
      "region" = ("where" #>> '{region,_eq}')
    )
  )
  AND (
    "where" #>> '{service_name,_eq}' IS NULL
    OR (
      "service_name" = ("where" #>> '{service_name,_eq}')
    )
  )
  AND (
    "where" #>> '{spend_date,_eq}' IS NULL
    OR (
      "spend_date"::date = ("where" #>> '{spend_date,_eq}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_gt}' IS NULL
    OR (
      "spend_date"::date > ("where" #>> '{spend_date,_eq}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_lt}' IS NULL
    OR (
      "spend_date"::date < ("where" #>> '{spend_date,_eq}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_le}' IS NULL
    OR (
      "spend_date"::date <= ("where" #>> '{spend_date,_eq}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_ge}' IS NULL
    OR (
      "spend_date"::date >= ("where" #>> '{spend_date,_eq}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_between,le}' IS NULL
    OR (
      "spend_date"::date <= ("where" #>> '{spend_date,_between,le}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_between,lt}' IS NULL
    OR (
      "spend_date"::date < ("where" #>> '{spend_date,_between,lt}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_between,gt}' IS NULL
    OR (
      "spend_date"::date > ("where" #>> '{spend_date,_between,gt}')::date
    )
  )
  AND (
    "where" #>> '{spend_date,_between,ge}' IS NULL
    OR (
      "spend_date"::date >= ("where" #>> '{spend_date,_between,ge}')::date
    )
  )
group by
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN tenant
    END
  ),
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN account
    END
  ),
  (
    CASE
      WHEN 'service_name' = ANY(group_by) THEN service_name
    END
  ),
  (
    CASE
      WHEN 'account_name' = ANY(group_by) THEN account_name
    END
  ),
  (
    CASE
      WHEN 'account_cloud_provider' = ANY(group_by) THEN account_cloud_provider
    END
  ),
  (
    CASE
      WHEN 'region' = ANY(group_by) THEN region
    END
  ),
  (
    CASE
      WHEN 'spend_date' = ANY(group_by) THEN spend_date
    END
  )
  $function$;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."spends_project_monthly_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."spends_project_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."spends_amount_sum_daily_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."spends_account_monthly_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."spends_account_date_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."compliance_check_findings_count_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."compliance_account_execution";


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- create or replace view k8s_account_resource_usage as
-- select tenant_id, cloud_account_id
--     , sum(case when metric = 'cpuCoreRequestAverage' then value end) as avg_cpu_request
--     , sum(case when metric = 'cpuCoreUsageAverage' then value end) as avg_cpu_usage
--     , sum(case when metric = 'ramByteRequestAverage' then value/
--     (1024*1024) end) as avg_mem_request
--     , sum(case when metric = 'ramByteUsageAverage' then value/
--     (1024*1024) end) as avg_mem_usage
--     , sum(case when metric = 'networkReceiveBytes' then value/
--     (1024*1024) end) as avg_ingress
--     , sum(case when metric = 'networkTransferBytes' then value/
--     (1024*1024) end) as avg_egress
-- from
-- (
--     select tenant_id, cloud_account_id, cloud_resource_id, "timestamp", metric, value, row_number () OVER (
--             PARTITION BY cloud_account_id, cloud_resource_id, metric
--     		ORDER BY "timestamp" DESC NULLS LAST
--     	) rank
--     from cloud_resource_metrics
--     where cloud_resource_id in (select cloud_resource_id from k8s_nodes where is_active = true)
-- ) n
-- where n.rank = 1
-- group by n.tenant_id, n.cloud_account_id;
