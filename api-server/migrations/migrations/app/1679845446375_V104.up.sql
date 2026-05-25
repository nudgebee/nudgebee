
CREATE
OR REPLACE VIEW "public"."cloudaccount_k8s_aggregate" AS
select 
    ca.id as id,
    ca.tenant as tenant,
	ca.account_name as account_name, 
	count(distinct case when cr.tags ->> 'controllerKind' is not null then cr.tags ->> 'controller' end) as count_workloads,
	count(distinct case when cr.tags ->> 'node' is not null then cr.tags ->> 'node' end) as count_hosts, 	
	count(distinct case when cr.tags ->> 'pod' is not null then cr.tags ->> 'pod' end) as count_pods
from cloud_accounts ca 
join cloud_resourses cr on cr.account = ca.id
join spends s on s.cloud_account  = ca.id
where ca.account_type = 'kubernetes'
group by ca.id, ca.account_name, ca.tenant;

CREATE
OR REPLACE VIEW "public"."cloudaccount_k8s_metrics_aggregate" AS
select 
    cr.tenant as tenant, 
    cr.account as account, 
    crm."timestamp" as "timestamp", 
    crm.metric as metric, 
    sum(crm.value) as value
from cloud_resource_metrics crm 
join cloud_resourses cr on cr.id = crm.cloud_resource_id 
where cr.account in (select id from cloud_accounts where account_type = 'kubernetes')
group by cr.tenant, cr.account, crm."timestamp", crm.metric;
