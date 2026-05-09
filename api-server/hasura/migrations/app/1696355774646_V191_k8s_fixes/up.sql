
DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_aggregate";
CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_aggregate" AS 
WITH cluster_ranked_nodes AS (
  SELECT
    cksna.tenant_id,
    cksna.account_id,
    (cksna."timestamp") :: date AS "timestamp",
    cksna.node_name,
    cksna.node_flavor,
    cksna.avg_cpu_used,
    cksna.max_cpu_used,
    cksna.avg_memory_used,
    cksna.max_memory_used,
    cksna.node_type,
    dense_rank() OVER (
      PARTITION BY cksna.tenant_id,
      cksna.account_id,
      cksna.node_name,
      cksna.node_flavor
      ORDER BY
        ((cksna."timestamp") :: date)
        DESC
    ) AS node_rank
  FROM
    cloudaccount_k8s_node_aggregate cksna
),
cluster_nodes AS (
  SELECT
    cksna.tenant_id,
    cksna.account_id,
    count(DISTINCT cksna.node_name) AS node_count,
    count(
      DISTINCT CASE
        WHEN (cksna.node_type = 'spot' :: text) THEN cksna.node_name
        ELSE NULL :: text
      END
    ) AS spot_node_count,
    count(
      DISTINCT CASE
        WHEN (cksna.node_type = 'on_demand' :: text) THEN cksna.node_name
        ELSE NULL :: text
      END
    ) AS ondemand_node_count,
    sum(
      CASE
        WHEN (cksna.node_rank = 1) THEN cksna.avg_cpu_used
        ELSE NULL :: double precision
      END
    ) AS avg_cpu_used_node,
    sum(
      CASE
        WHEN (cksna.node_rank = 1) THEN cksna.max_cpu_used
        ELSE NULL :: double precision
      END
    ) AS max_cpu_used_node,
    sum(
      CASE
        WHEN (cksna.node_rank = 1) THEN ((crd.resource_capacity ->> 'cpu_virtual' :: text)) :: integer
        ELSE NULL :: integer
      END
    ) AS total_cpu_capacity,
    sum(
      CASE
        WHEN (cksna.node_rank = 1) THEN cksna.avg_memory_used
        ELSE NULL :: double precision
      END
    ) AS avg_memory_used_node,
    sum(
      CASE
        WHEN (cksna.node_rank = 1) THEN cksna.max_memory_used
        ELSE NULL :: double precision
      END
    ) AS max_memory_used_node,
    sum(
      CASE
        WHEN (cksna.node_rank = 1) THEN ((crd.resource_capacity ->> 'memory_gb' :: text)) :: integer
        ELSE NULL :: integer
      END
    ) AS total_memory_capacity,
    cksna."timestamp" as "timestamp"
  FROM
    (
      cluster_ranked_nodes cksna
      LEFT JOIN cloud_resource_details crd ON ((crd.resource_type = cksna.node_flavor and crd.resource_region='us-east-1') and cksna.node_rank=1)
    )
  GROUP BY
    cksna.tenant_id,
    cksna.account_id,
    cksna."timestamp"
  ORDER BY
    cksna.tenant_id,
    cksna.account_id DESC
),
cluster_pods AS (
  SELECT
    ckspa.tenant_id,
    ckspa.account_id,
    count(DISTINCT ckspa.pod_name) AS pod_count,
    count(
      CASE
        WHEN (ckspa.is_active = false) THEN 1
        ELSE NULL :: integer
      END
    ) AS failed_pod_count,
    count(
      DISTINCT (
        (ckspa.namespace_name || '.' :: text) || ckspa.workload_name
      )
    ) AS workload_count,
    sum(ckspa.pod_cost) AS pod_cost
  FROM
    cloudaccount_k8s_pod_aggregate ckspa
  GROUP BY
    ckspa.tenant_id,
    ckspa.account_id
),
cloud_spend_mtd AS (
  SELECT
    sum(s.amount) AS mtd_cost,
    ca_1.id AS cloud_account_id
  FROM
    spends s,
    cloud_accounts ca_1
  WHERE
    (
      (s.cloud_account = ca_1.id)
      AND (ca_1.account_type = 'kubernetes' :: text)
      AND (
        date_trunc('month' :: text, s.date) = date_trunc(
          'month' :: text,
          (CURRENT_DATE) :: timestamp with time zone
        )
      )
      AND (
        date_trunc('year' :: text, s.date) = date_trunc(
          'year' :: text,
          (CURRENT_DATE) :: timestamp with time zone
        )
      )
    )
  GROUP BY
    ca_1.id
)
SELECT
  cn.tenant_id,
  cn.account_id,
  ca.account_name,
  cn.node_count,
  cn.spot_node_count,
  cn.ondemand_node_count,
  cn.avg_cpu_used_node,
  cn.max_cpu_used_node,
  cn.avg_memory_used_node,
  cn.max_memory_used_node,
  cp.workload_count,
  cp.pod_count,
  cp.failed_pod_count,
  cp.pod_cost,
  cn.total_cpu_capacity,
  cn.total_memory_capacity,
  csm.mtd_cost,
  cn."timestamp",
  ROW_NUMBER() OVER(PARTITION BY cn.tenant_id, cn.account_id ORDER BY cn."timestamp" DESC) AS rn
FROM
  (
    (
      (
        cluster_nodes cn
        JOIN cluster_pods cp ON (
          (
            (cn.tenant_id = cp.tenant_id)
            AND (cn.account_id = cp.account_id)
          )
        )
      )
      JOIN cloud_accounts ca ON (
        (
          (cn.tenant_id = ca.tenant)
          AND (cn.account_id = ca.id)
        )
      )
    )
    
    JOIN cloud_spend_mtd csm ON ((csm.cloud_account_id = ca.id))
  );

alter table "public"."cloud_account_score" add constraint "cloud_account_score_cloud_account_id_tenant_score_key" unique ("cloud_account_id", "tenant", "score");
