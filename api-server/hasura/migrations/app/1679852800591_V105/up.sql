
CREATE OR REPLACE VIEW "public"."cloudaccount_k8s_workload_aggregate" AS 
    select ca.id,
    	ca.tenant,
    	cr.tags ->> 'controllerKind' as workload_type,
    	cr.tags ->> 'controller' as workload_name,
    	s."date" as timestamp,
    	sum(s.amount) as workload_cost,
    	avg(case when crm.metric = 'cpuCoreUsageAverage' then crm.value end) as cpu_used,
    	avg(case when crm.metric = 'ramByteUsageAverage' then crm.value end) as memory_used,
    	avg(case when crm.metric = 'cpuCoreRequestAverage' then crm.value end) as cpu_request,
    	avg(case when crm.metric = 'ramByteRequestAverage' then crm.value end) as memory_request,
    	avg(case when crm.metric = 'cpuEfficiency' then crm.value end) as cpu_efficiency,
    	avg(case when crm.metric = 'ramEfficiency' then crm.value end) as ram_efficiency
    from cloud_accounts ca 
    join cloud_resourses cr on cr.account = ca.id
    join spends s on s.cloud_account  = ca.id and s.cloud_resource_id = cr.id 
    join cloud_resource_metrics crm on crm.cloud_resource_id = cr.id 
    where ca.account_type = 'kubernetes' 
    	and cr.tags ->> 'controllerKind' is not null
    	and crm.metric in ('cpuCoreUsageAverage', 'ramByteUsageAverage', 'cpuCoreRequestAverage', 'ramByteRequestAverage')
    group by ca.id, ca.tenant, cr.tags ->> 'controllerKind', cr.tags ->> 'controller', s."date";
