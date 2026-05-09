
DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_aggregate";
CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_aggregate" AS 
 WITH cluster_ranked_nodes AS (
         SELECT cksna.tenant_id,
            cksna.account_id,
            (cksna."timestamp")::date AS "timestamp",
            cksna.node_name,
            cksna.node_flavor,
            cksna.avg_cpu_used,
            cksna.max_cpu_used,
            cksna.avg_memory_used,
            cksna.max_memory_used,
            cksna.node_type,
            dense_rank() OVER (PARTITION BY cksna.tenant_id, cksna.account_id, ((cksna."timestamp")::date), cksna.node_name, cksna.node_flavor ORDER BY cksna.tenant_id, cksna.account_id, ((cksna."timestamp")::date), cksna.node_name, cksna.node_flavor DESC) AS node_rank
           FROM cloudaccount_k8s_node_aggregate cksna
        ), cluster_nodes AS (
         SELECT cksna.tenant_id,
            cksna.account_id,
            count(DISTINCT cksna.node_name) AS node_count,
            count(DISTINCT
                CASE
                    WHEN (cksna.node_type = 'spot'::text) THEN cksna.node_name
                    ELSE NULL::text
                END) AS spot_node_count,
            count(DISTINCT
                CASE
                    WHEN (cksna.node_type = 'on_demand'::text) THEN cksna.node_name
                    ELSE NULL::text
                END) AS ondemand_node_count,
            sum(
                CASE
                    WHEN (cksna.node_rank = 1) THEN cksna.avg_cpu_used
                    ELSE NULL::double precision
                END) AS avg_cpu_used_node,
            sum(
                CASE
                    WHEN (cksna.node_rank = 1) THEN cksna.max_cpu_used
                    ELSE NULL::double precision
                END) AS max_cpu_used_node,
            sum(
                CASE
                    WHEN (cksna.node_rank = 1) THEN ((crd.resource_capacity ->> 'cpu_virtual'::text))::integer
                    ELSE NULL::integer
                END) AS total_cpu_capacity,
            sum(
                CASE
                    WHEN (cksna.node_rank = 1) THEN cksna.avg_memory_used
                    ELSE NULL::double precision
                END) AS avg_memory_used_node,
            sum(
                CASE
                    WHEN (cksna.node_rank = 1) THEN cksna.max_memory_used
                    ELSE NULL::double precision
                END) AS max_memory_used_node,
            sum(
                CASE
                    WHEN (cksna.node_rank = 1) THEN ((crd.resource_capacity ->> 'memory_gb'::text))::integer
                    ELSE NULL::integer
                END) AS total_memory_capacity
           FROM (cluster_ranked_nodes cksna
             LEFT JOIN cloud_resource_details crd ON ((crd.resource_type = cksna.node_flavor)))
          GROUP BY cksna.tenant_id, cksna.account_id
          ORDER BY cksna.tenant_id, cksna.account_id DESC
        ), cluster_pods AS (
         SELECT ckspa.tenant_id,
            ckspa.account_id,
            count(DISTINCT ckspa.pod_name) AS pod_count,
            count(
                CASE
                    WHEN (ckspa.is_active = false) THEN 1
                    ELSE NULL::integer
                END) AS failed_pod_count,
            count(DISTINCT ((ckspa.namespace_name || '.'::text) || ckspa.workload_name)) AS workload_count,
            sum(ckspa.pod_cost) AS pod_cost
           FROM cloudaccount_k8s_pod_aggregate ckspa
          GROUP BY ckspa.tenant_id, ckspa.account_id
        )
 SELECT cn.tenant_id,
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
    cp.pod_cost
   FROM ((cluster_nodes cn
     JOIN cluster_pods cp ON (((cn.tenant_id = cp.tenant_id) AND (cn.account_id = cp.account_id))))
     JOIN cloud_accounts ca ON (((cn.tenant_id = ca.tenant) AND (cn.account_id = ca.id))));

DROP MATERIALIZED VIEW "public"."cloudaccount_k8s_aggregate";
CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_aggregate" AS 
 with cluster_ranked_nodes as (
select
	cksna.tenant_id,
	cksna.account_id,
	(cksna."timestamp") :: date as "timestamp",
	cksna.node_name,
	cksna.node_flavor,
	cksna.avg_cpu_used,
	cksna.max_cpu_used,
	cksna.avg_memory_used,
	cksna.max_memory_used,
	cksna.node_type,
	dense_rank() over (
      partition by cksna.tenant_id,
	cksna.account_id,
	((cksna."timestamp") :: date),
	cksna.node_name,
	cksna.node_flavor
order by
	cksna.tenant_id,
	cksna.account_id,
	((cksna."timestamp") :: date),
	cksna.node_name,
	cksna.node_flavor desc
    ) as node_rank
from
	cloudaccount_k8s_node_aggregate cksna
),
cluster_nodes as (
select
	cksna.tenant_id,
	cksna.account_id,
	count(distinct cksna.node_name) as node_count,
	count(
      distinct case
        when (cksna.node_type = 'spot' :: text) then cksna.node_name
        else null :: text
      end
    ) as spot_node_count,
	count(
      distinct case
        when (cksna.node_type = 'on_demand' :: text) then cksna.node_name
        else null :: text
      end
    ) as ondemand_node_count,
	sum(
      case
        when (cksna.node_rank = 1) then cksna.avg_cpu_used
        else null :: double precision
      end
    ) as avg_cpu_used_node,
	sum(
      case
        when (cksna.node_rank = 1) then cksna.max_cpu_used
        else null :: double precision
      end
    ) as max_cpu_used_node,
	sum(
      case
        when (cksna.node_rank = 1) then ((crd.resource_capacity ->> 'cpu_virtual' :: text)) :: integer
        else null :: integer
      end
    ) as total_cpu_capacity,
	sum(
      case
        when (cksna.node_rank = 1) then cksna.avg_memory_used
        else null :: double precision
      end
    ) as avg_memory_used_node,
	sum(
      case
        when (cksna.node_rank = 1) then cksna.max_memory_used
        else null :: double precision
      end
    ) as max_memory_used_node,
	sum(
      case
        when (cksna.node_rank = 1) then ((crd.resource_capacity ->> 'memory_gb' :: text)) :: integer
        else null :: integer
      end
    ) as total_memory_capacity
from
	(
      cluster_ranked_nodes cksna
left join cloud_resource_details crd on
	((crd.resource_type = cksna.node_flavor))
    )
group by
	cksna.tenant_id,
	cksna.account_id
order by
	cksna.tenant_id,
	cksna.account_id desc
),
cluster_pods as (
select
	ckspa.tenant_id,
	ckspa.account_id,
	count(distinct ckspa.pod_name) as pod_count,
	count(
      case
        when (ckspa.is_active = false) then 1
        else null :: integer
      end
    ) as failed_pod_count,
	count(
      distinct (
        (ckspa.namespace_name || '.' :: text) || ckspa.workload_name
      )
    ) as workload_count,
	sum(ckspa.pod_cost) as pod_cost
from
	cloudaccount_k8s_pod_aggregate ckspa
group by
	ckspa.tenant_id,
	ckspa.account_id
), 
cloud_spend_mtd as (
select
	sum(amount) as mtd_cost,
	ca.id as cloud_account_id
from
	spends s ,
	cloud_accounts ca
where
	s.cloud_account = ca.id 
	and ca.account_type = 'kubernetes'::text
	and DATE_TRUNC('month', date) = DATE_TRUNC('month', CURRENT_DATE)
	and DATE_TRUNC('year', date) = DATE_TRUNC('year', CURRENT_DATE)
group by ca.id
)
select
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
	csm.mtd_cost
from
	(
    (
      cluster_nodes cn
join cluster_pods cp on
	(
        (
          (cn.tenant_id = cp.tenant_id)
		and (cn.account_id = cp.account_id)
        )
      )
    )
join cloud_accounts ca on
	(
      (
        (cn.tenant_id = ca.tenant)
		and (cn.account_id = ca.id)
      )
    )
  )
  join cloud_spend_mtd csm on (
  csm.cloud_account_id = ca.id
  );
