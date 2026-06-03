DROP MATERIALIZED VIEW public.cloudaccount_k8s_resource_aggregate;
DROP MATERIALIZED VIEW public.cloudaccount_k8s_resource_node_aggregate;

CREATE MATERIALIZED VIEW public.cloudaccount_k8s_resource_node_aggregate
TABLESPACE pg_default
AS 	select
	cr.tenant as tenant_id,
	cr.account as account_id,
	cr.name,
	cr.id,
	cr.is_active,
	s.date as "timestamp",
	min(cr.first_seen) as node_creation_time,
	cr.meta ->> 'conditions'::text as conditions,
	case
		when (((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text) is not null then ((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text
		when (((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'eks.amazonaws.com/capacityType'::text) is not null then ((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'eks.amazonaws.com/capacityType'::text
		else 'on-demand'::text
	end as node_type,
	((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text as node_flavor,
	((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'topology.kubernetes.io/region'::text as node_region,
	((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'topology.kubernetes.io/zone'::text as node_zone,
	sum(s.amount) as node_cost,
	sum(s.amount) as workload_cost,
	avg(crm.avg_cpu_used) as avg_cpu_used,
	max(crm.max_cpu_used) as max_cpu_used,
	avg(crm.avg_memory_used) as avg_memory_used,
	max(crm.max_memory_used) as max_memory_used,
	avg(crm.avg_cpu_request) as avg_cpu_request,
	max(crm.max_cpu_request) as max_cpu_request,
	avg(crm.avg_memory_request) as avg_memory_request,
	max(crm.max_memory_request) as max_memory_request,
	avg(crm.avg_cpu_efficiency) as avg_cpu_efficiency,
	max(crm.max_cpu_efficiency) as max_cpu_efficiency,
	avg(crm.avg_ram_efficiency) as avg_ram_efficiency,
	max(crm.max_ram_efficiency) as max_ram_efficiency,
	max(crm.max_total_efficiency) as max_total_efficiency,
	max(cr.meta ->> 'cpu_allocated'::text)::double precision as cpu_allocated,
	max(cr.meta ->> 'memory_capacity'::text)::double precision as memory_capacity,
	max(cr.meta ->> 'cpu_capacity'::text)::double precision as cpu_capacity,
	max(cr.meta ->> 'memory_allocatable'::text)::double precision as memory_allocatable,
	max(cr.meta ->> 'cpu_allocatable'::text)::double precision as cpu_allocatable,
	max(cr.meta ->> 'memory_allocated'::text)::double precision as memory_allocated,
	max(cr.meta ->> 'pods_count'::text)::integer as pods_count,
	max(crd.resource_cost) as resource_cost_per_hour,
	sum(crm.total_cpu_cost) as total_cpu_cost,
	sum(crm.total_ram_cost) as total_ram_cost,
	sum(crm.total_gpu_cost) as total_gpu_cost
from
	cloud_resourses cr
join spends s on
	s.cloud_account = cr.account
	and s.cloud_resource_id = cr.id
left join cloudaccount_k8s_resource_metrics_aggregate crm on
	crm.cloud_resource_id = cr.id
	and s.date = crm."timestamp"
left join cloud_resource_details crd on
	crd.resource_type = (((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text)
	and crd.resource_region = (((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'topology.kubernetes.io/region'::text)
where
	lower(cr.type) = 'node'::text
group by
	cr.id,
	cr.account,
	cr.tenant,
	cr.name,
	s.date,
	((cr.tags -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text),
	((cr.tags -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text),
	(cr.meta ->> 'conditions'::text);
	
CREATE UNIQUE INDEX cloudaccount_k8s_resource_node_aggregate_pk ON public.cloudaccount_k8s_resource_node_aggregate USING btree (id, tenant_id, account_id, "timestamp");


-- public.cloudaccount_k8s_resource_aggregate source

CREATE MATERIALIZED VIEW public.cloudaccount_k8s_resource_aggregate
TABLESPACE pg_default
AS WITH cluster_nodes AS (
         SELECT cksna.tenant_id,
            cksna.account_id,
            count(DISTINCT
                CASE
                    WHEN cksna.is_active IS NOT FALSE THEN cksna.name
                    ELSE NULL::text
                END) AS node_count,
            count(DISTINCT
                CASE
                    WHEN lower(cksna.node_type) = 'spot'::text AND cksna.is_active IS NOT FALSE THEN cksna.name
                    ELSE NULL::text
                END) AS spot_node_count,
            count(DISTINCT
                CASE
                    WHEN lower(cksna.node_type) = 'on-demand'::text OR lower(cksna.node_type) = 'on_demand'::text AND cksna.is_active IS NOT FALSE THEN cksna.name
                    ELSE NULL::text
                END) AS ondemand_node_count,
            sum(cksna.avg_cpu_used) AS avg_cpu_used_node,
            sum(cksna.max_cpu_used) AS max_cpu_used_node,
            sum(
                CASE
                    WHEN cksna.is_active IS NOT FALSE THEN cksna.cpu_capacity
                    ELSE NULL::double precision
                END) AS total_cpu_capacity,
            sum(cksna.avg_memory_used) AS avg_memory_used_node,
            sum(cksna.max_memory_used) AS max_memory_used_node,
            sum(
                CASE
                    WHEN cksna.is_active IS NOT FALSE THEN cksna.memory_capacity
                    ELSE NULL::double precision
                END) AS total_memory_capacity,
            sum(
                CASE
                    WHEN cksna.is_active IS NOT FALSE THEN cksna.memory_allocatable
                    ELSE NULL::double precision
                END) AS total_memory_allocatable,
            sum(
                CASE
                    WHEN cksna.is_active IS NOT FALSE THEN cksna.cpu_allocatable
                    ELSE NULL::double precision
                END) AS total_cpu_allocatable,
            sum(
                CASE
                    WHEN cksna.is_active IS NOT FALSE THEN cksna.cpu_allocated
                    ELSE NULL::double precision
                END) AS total_cpu_allocated,
            sum(
                CASE
                    WHEN cksna.is_active IS NOT FALSE THEN cksna.memory_allocated
                    ELSE NULL::double precision
                END) AS total_memory_allocated,
            sum(
                CASE
                    WHEN cksna.is_active IS NOT FALSE THEN cksna.pods_count::double precision
                    ELSE NULL::double precision
                END) AS pods_count,
            cksna."timestamp",
            sum(cksna.node_cost) AS node_cost,
            avg(
                CASE
                    WHEN cksna.is_active IS NOT FALSE THEN cksna.max_total_efficiency
                    ELSE NULL::double precision
                END) AS total_efficiency
           FROM cloudaccount_k8s_resource_node_aggregate cksna
          GROUP BY cksna.tenant_id, cksna.account_id, cksna."timestamp"
        ), cluster_pods AS (
         SELECT ckspa.tenant_id,
            ckspa.account_id,
            count(DISTINCT ckspa.pod_name) AS pod_count,
            count(
                CASE
                    WHEN ckspa.is_active = false THEN 1
                    ELSE NULL::integer
                END) AS failed_pod_count,
            count(DISTINCT (ckspa.namespace_name || '.'::text) || ckspa.workload_name) AS workload_count,
            sum(ckspa.pod_cost) AS pod_cost,
            ckspa."timestamp"
           FROM cloudaccount_k8s_resource_pod_aggregate ckspa
          GROUP BY ckspa.tenant_id, ckspa.account_id, ckspa."timestamp"
        ), cloud_spend_mtd AS (
         SELECT sum(cn_1.node_cost) AS mtd_cost,
            cn_1.account_id AS cloud_account_id
           FROM cloudaccount_k8s_resource_node_aggregate cn_1
          WHERE date_trunc('month'::text, cn_1."timestamp") = date_trunc('month'::text, CURRENT_DATE::timestamp with time zone) AND date_trunc('year'::text, cn_1."timestamp") = date_trunc('year'::text, CURRENT_DATE::timestamp with time zone)
          GROUP BY cn_1.account_id
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
    cn.pods_count AS pod_count,
    cp.failed_pod_count,
    cp.pod_cost,
    cn.total_cpu_capacity,
    cn.total_cpu_allocatable,
    cn.total_memory_capacity,
    cn.total_memory_allocatable,
    cn.total_memory_allocated,
    cn.total_cpu_allocated,
    csm.mtd_cost,
    cn."timestamp",
    cn.node_cost,
     cn.node_cost as workload_cost,
    row_number() OVER (PARTITION BY cn.tenant_id, cn.account_id ORDER BY cn."timestamp" DESC) AS rn,
    crsa.best_practice_score,
    crsa.right_sizing_score,
    cn.total_efficiency,
    0 as total_idle_cost
   FROM cluster_nodes cn
     LEFT JOIN cluster_pods cp ON cn.tenant_id = cp.tenant_id AND cn.account_id = cp.account_id AND cn."timestamp" = cp."timestamp"
     JOIN cloud_accounts ca ON cn.tenant_id = ca.tenant AND cn.account_id = ca.id
     LEFT JOIN cloud_spend_mtd csm ON csm.cloud_account_id = ca.id
     LEFT JOIN cloudaccount_k8s_resource_score_aggregate crsa ON crsa.cloud_account_id = ca.id
WITH DATA;

-- View indexes:
CREATE UNIQUE INDEX cloudaccount_k8s_resource_aggregate_pk ON public.cloudaccount_k8s_resource_aggregate USING btree (tenant_id, account_id, "timestamp");
