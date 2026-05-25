DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_resource_aggregate";
CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_aggregate" AS 
 	with cluster_nodes as (
select
	cksna.tenant_id,
	cksna.account_id,
	count(distinct case when cksna.is_active is not false then cksna.name else null::text end) as node_count,
	count(distinct
                case
                    when lower(cksna.node_type) = 'spot'::text and cksna.is_active is not false then cksna.name
                    else null::text
                end) as spot_node_count,
	count(distinct
                case
                    when lower(cksna.node_type) = 'on-demand'::text and cksna.is_active is not false then cksna.name
                    else null::text
                end) as ondemand_node_count,
	sum(cksna.avg_cpu_used) as avg_cpu_used_node,
	sum(cksna.max_cpu_used) as max_cpu_used_node,
	sum(
                case
                    when cksna.is_active is not false then cksna.cpu_capacity
                    else null::double precision
                end) as total_cpu_capacity,
	sum(cksna.avg_memory_used) as avg_memory_used_node,
	sum(cksna.max_memory_used) as max_memory_used_node,
	sum(
                case
                    when cksna.is_active is not false then cksna.memory_capacity
                    else null::double precision
                end) as total_memory_capacity,
    	sum(
                case
                    when cksna.is_active is not false then cksna.memory_allocatable
                    else null::double precision
                end) as total_memory_allocatable,
          	sum(
                case
                    when cksna.is_active is not false then cksna.cpu_allocatable
                    else null::double precision
                end) as total_cpu_allocatable ,
                       	sum(
                case
                    when cksna.is_active is not false then cksna.pods_count
                    else null::double precision
                end) as pods_count ,
	cksna."timestamp"
from
	cloudaccount_k8s_resource_node_aggregate cksna
left join cloud_resource_details crd on
	crd.resource_type = cksna.node_flavor
	and crd.resource_region = 'us-east-1'::text
group by
	cksna.tenant_id,
	cksna.account_id,
	cksna."timestamp"
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
                    when ckspa.is_active = false then 1
                    else null::integer
                end) as failed_pod_count,
	count(distinct (ckspa.namespace_name || '.'::text) || ckspa.workload_name) as workload_count,
	sum(ckspa.pod_cost) as pod_cost
from
	cloudaccount_k8s_resource_pod_aggregate ckspa
group by
	ckspa.tenant_id,
	ckspa.account_id
        ),
cloud_spend_mtd as (
select
	sum(s.amount) as mtd_cost,
	ca_1.id as cloud_account_id
from
	spends s,
	cloud_accounts ca_1
where
	s.cloud_account = ca_1.id
	and ca_1.cloud_provider = 'K8s'::text
	and date_trunc('month'::text, s.date) = date_trunc('month'::text, CURRENT_DATE::timestamp with time zone)
		and date_trunc('year'::text, s.date) = date_trunc('year'::text, CURRENT_DATE::timestamp with time zone)
	group by
		ca_1.id
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
	cn.pods_count as pod_count,
	cp.failed_pod_count,
	cp.pod_cost,
	cn.total_cpu_capacity,
	cn.total_cpu_allocatable,
	cn.total_memory_capacity,
	cn.total_memory_allocatable,
	csm.mtd_cost,
	cn."timestamp",
	row_number() over (partition by cn.tenant_id,
	cn.account_id
order by
	cn."timestamp" desc) as rn
from
	cluster_nodes cn
left join cluster_pods cp on
	cn.tenant_id = cp.tenant_id
	and cn.account_id = cp.account_id
join cloud_accounts ca on
	cn.tenant_id = ca.tenant
	and cn.account_id = ca.id
left join cloud_spend_mtd csm on
	csm.cloud_account_id = ca.id;
	
CREATE UNIQUE INDEX cloudaccount_k8s_resource_aggregate_pk ON public.cloudaccount_k8s_resource_aggregate USING btree (tenant_id, account_id, "timestamp");
