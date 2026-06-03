-- public.cloudaccount_k8s_resource_workload_aggregate source

CREATE MATERIALIZED VIEW public.cloudaccount_k8s_resource_workload_aggregate
TABLESPACE pg_default
AS select
	ca.tenant as tenant_id,
	ca.id as account_id,
	cr2.external_resource_id as external_workload_id,
	(cr.meta ->> 'namespace' :: text) as namespace_name,
	(cr.meta ->> 'controllerKind' :: text) as workload_type,
	(cr.meta ->> 'controller' :: text) as workload_name,
	s.date as "timestamp",
	cr2.is_active ,
	max(cr2.meta ->> 'total_pods' :: text) as total_pods,
	max(cr2.meta ->> 'ready_pods' :: text) as ready_pods,
	max(cr2.meta ->> 'first_seen') as node_creation_time,
	sum(s.amount) as workload_cost,
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
	max(max_ram_efficiency) as max_ram_efficiency
from
	(
    (
      (
        cloud_accounts ca
join cloud_resourses cr on
	((cr.account = ca.id))
      )
left join spends s on
	(
        (
          (s.cloud_account = ca.id)
		and (s.cloud_resource_id = cr.id)
        )
      )
    )
left join cloudaccount_k8s_resource_metrics_aggregate crm on
	((crm.cloud_resource_id = cr.id)
		and s."date" = crm."timestamp")
  )
join cloud_resourses cr2 on
	(
	cr.account = cr2.account
		and (cr.meta ->> 'controller' :: text = cr2."name" )
			and (cr.meta ->> 'controllerKind' :: text = cr2."type" )
			and (cr.meta ->> 'namespace' :: text) = (cr2.meta ->> 'namespace' :: text)
)
where
	(
    (ca.account_type = 'kubernetes' :: text)
		and (lower(cr.type) = 'pod' :: text)
			and (cr.is_active is not false)
				and ((cr.meta ->> 'controllerKind' :: text) is not null)
  )
group by
    cr2.id,
	ca.id,
	ca.tenant,
	(cr.meta ->> 'controllerKind' :: text),
	(cr.meta ->> 'controller' :: text),
	(cr.meta ->> 'namespace' :: text),
	(cr2.meta ->> 'total_pods' :: text),
	(cr2.meta ->> 'ready_pods' :: text),
	cr2.is_active,
	s.date
WITH DATA;

-- View indexes:
CREATE UNIQUE INDEX cloudaccount_k8s_resource_workload_aggregate_pk ON public.cloudaccount_k8s_resource_workload_aggregate USING btree (external_workload_id, tenant_id, account_id, "timestamp");
