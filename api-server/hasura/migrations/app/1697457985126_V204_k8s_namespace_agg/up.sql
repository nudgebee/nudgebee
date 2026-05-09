CREATE MATERIALIZED VIEW public.cloudaccount_k8s_resource_namespace_aggregate
TABLESPACE pg_default
AS select
	ca.tenant as tenant_id,
	ca.id as account_id,
	cksrpa.namespace_name as namespace_name,
	cksrpa."timestamp" as "timestamp",
	sum(cksrpa.pod_cost) as namespace_cost,
	avg(avg_cpu_used) as avg_cpu_used,
	max(max_cpu_used) as max_cpu_used,
	avg(avg_memory_used) as avg_memory_used,
	max(max_memory_used) as max_memory_used,
	avg(avg_cpu_request) as avg_cpu_request,
	max(max_cpu_request) as max_cpu_request,
	avg(avg_memory_request) as avg_memory_request,
	max(max_memory_request) as max_memory_request,
	avg(avg_cpu_efficiency) as avg_cpu_efficiency,
	max(max_cpu_efficiency) as max_cpu_efficiency,
	avg(avg_ram_efficiency) as avg_ram_efficiency,
	max(max_ram_efficiency) as max_ram_efficiency,
	count(*) as container_count ,
	string_agg(cksrpa.pod_name , ', ') as pod_names
from
	cloud_accounts ca
join cloudaccount_k8s_resource_pod_aggregate cksrpa 
on
	ca.id = cksrpa.account_id
group by
	ca.id,
	ca.tenant,
	cksrpa.namespace_name,
	cksrpa."timestamp" 
WITH DATA;

-- View indexes:
CREATE UNIQUE INDEX cloudaccount_k8s_resource_namespace_aggregatepk ON public.cloudaccount_k8s_resource_namespace_aggregate USING btree (tenant_id, account_id, namespace_name, "timestamp");
