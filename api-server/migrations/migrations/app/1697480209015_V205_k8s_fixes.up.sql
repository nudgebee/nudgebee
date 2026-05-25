
DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_resource_aggregate";
DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_resource_namespace_aggregate";
DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_resource_pod_aggregate";

CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_pod_aggregate" AS 
 SELECT cr.tenant AS tenant_id,
    cr.account AS account_id,
    cr.id,
    cr.name AS pod_name,
    cr.is_active,
    s.date AS "timestamp",
    (cr.meta ->> 'controller'::text) AS workload_name,
    (cr.meta ->> 'controllerKind'::text) AS workload_type,
    (cr.meta ->> 'namespace'::text) AS namespace_name,
    (cr.meta ->> 'status'::text) AS status,
    (cr.meta ->> 'restart_count'::text) AS restart_count,
    cr.first_seen AS creation_time,
    (cr.meta ->> 'node'::text) AS node_name,
        CASE
            WHEN (((intnc.meta -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text) IS NOT NULL) THEN ((intnc.meta -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text)
            ELSE 'on-demand'::text
        END AS node_type,
    ((intnc.meta -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text) AS node_flavor,
    sum(s.amount) AS pod_cost,
    avg(crm.avg_cpu_used) AS avg_cpu_used,
    max(crm.max_cpu_used) AS max_cpu_used,
    avg(crm.avg_memory_used) AS avg_memory_used,
    max(crm.max_memory_used) AS max_memory_used,
    avg(crm.avg_cpu_request) AS avg_cpu_request,
    max(crm.max_cpu_request) AS max_cpu_request,
    avg(crm.avg_memory_request) AS avg_memory_request,
    max(crm.max_memory_request) AS max_memory_request,
    avg(crm.avg_cpu_efficiency) AS avg_cpu_efficiency,
    max(crm.max_cpu_efficiency) AS max_cpu_efficiency,
    avg(crm.avg_ram_efficiency) AS avg_ram_efficiency,
    max(crm.max_ram_efficiency) AS max_ram_efficiency,
    cr.tags
   FROM (((cloud_resourses cr
     LEFT JOIN spends s ON (((s.cloud_account = cr.account) AND (s.cloud_resource_id = cr.id))))
     LEFT JOIN cloudaccount_k8s_resource_metrics_aggregate crm ON (((crm.cloud_resource_id = cr.id) AND (crm."timestamp" = s.date))))
     LEFT JOIN cloud_resourses intnc ON (((intnc.tenant = cr.tenant) AND (intnc.account = cr.account) AND ((cr.meta ->> 'node'::text) = intnc.name))))
  WHERE ((lower(cr.type) = 'pod'::text) AND ((cr.meta ->> 'node'::text) IS NOT NULL))
  GROUP BY cr.account, cr.tenant, cr.id, cr.name, (cr.meta ->> 'node'::text), ((intnc.meta -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text), ((intnc.meta -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text), s.date;
  
  CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_namespace_aggregate" AS
SELECT
  ca.tenant AS tenant_id,
  ca.id AS account_id,
  cksrpa.namespace_name,
  cksrpa."timestamp",
  sum(cksrpa.pod_cost) AS namespace_cost,
  avg(cksrpa.avg_cpu_used) AS avg_cpu_used,
  max(cksrpa.max_cpu_used) AS max_cpu_used,
  avg(cksrpa.avg_memory_used) AS avg_memory_used,
  max(cksrpa.max_memory_used) AS max_memory_used,
  avg(cksrpa.avg_cpu_request) AS avg_cpu_request,
  max(cksrpa.max_cpu_request) AS max_cpu_request,
  avg(cksrpa.avg_memory_request) AS avg_memory_request,
  max(cksrpa.max_memory_request) AS max_memory_request,
  avg(cksrpa.avg_cpu_efficiency) AS avg_cpu_efficiency,
  max(cksrpa.max_cpu_efficiency) AS max_cpu_efficiency,
  avg(cksrpa.avg_ram_efficiency) AS avg_ram_efficiency,
  max(cksrpa.max_ram_efficiency) AS max_ram_efficiency,
  count(*) AS container_count,
  string_agg(cksrpa.pod_name, ', ' :: text) AS pod_names
FROM
  (
    cloud_accounts ca
    JOIN cloudaccount_k8s_resource_pod_aggregate cksrpa ON ((ca.id = cksrpa.account_id))
  )
GROUP BY
  ca.id,
  ca.tenant,
  cksrpa.namespace_name,
  cksrpa."timestamp";
  
CREATE UNIQUE INDEX cloudaccount_k8s_resource_pod_aggregate_pk ON public.cloudaccount_k8s_resource_pod_aggregate USING btree (id, tenant_id, account_id, "timestamp");
CREATE UNIQUE INDEX cloudaccount_k8s_resource_namespace_aggregate_pk ON public.cloudaccount_k8s_resource_namespace_aggregate USING btree (tenant_id, account_id, namespace_name, "timestamp");

CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_score_aggregate" AS 
select
    ca.tenant,
	cas.cloud_account_id as cloud_account_id,
	max(case when cas."source" = 'popeye' then cas.score else null end) as best_practice_score,
	max(case when cas."source" = 'krr' then cas.score else null end) as right_sizing_score
from
	cloud_accounts ca
inner join cloud_account_score cas 
	on
	cas.cloud_account_id = ca.id
where 
	ca.cloud_provider = 'K8s'
group by
	cas.cloud_account_id ,
	ca.tenant;

DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_resource_namespace_aggregate";
DROP MATERIALIZED VIEW IF EXISTS "public"."cloudaccount_k8s_resource_pod_aggregate";

CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_pod_aggregate" AS 
 SELECT cr.tenant AS tenant_id,
    cr.account AS account_id,
    cr.id,
    cr.name AS pod_name,
    cr.is_active,
    s.date AS "timestamp",
    (cr.meta ->> 'controller'::text) AS workload_name,
    (cr.meta ->> 'controllerKind'::text) AS workload_type,
    (cr.meta ->> 'namespace'::text) AS namespace_name,
    (cr.meta ->> 'status'::text) AS status,
    (cr.meta ->> 'restart_count'::text) AS restart_count,
    cr.first_seen AS creation_time,
    (cr.meta ->> 'node'::text) AS node_name,
        CASE
            WHEN (((intnc.meta -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text) IS NOT NULL) THEN ((intnc.meta -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text)
            ELSE 'on-demand'::text
        END AS node_type,
    ((intnc.meta -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text) AS node_flavor,
    sum(s.amount) AS pod_cost,
    avg(crm.avg_cpu_used) AS avg_cpu_used,
    max(crm.max_cpu_used) AS max_cpu_used,
    avg(crm.avg_memory_used) AS avg_memory_used,
    max(crm.max_memory_used) AS max_memory_used,
    avg(crm.avg_cpu_request) AS avg_cpu_request,
    max(crm.max_cpu_request) AS max_cpu_request,
    avg(crm.avg_memory_request) AS avg_memory_request,
    max(crm.max_memory_request) AS max_memory_request,
    avg(crm.avg_cpu_efficiency) AS avg_cpu_efficiency,
    max(crm.max_cpu_efficiency) AS max_cpu_efficiency,
    avg(crm.avg_ram_efficiency) AS avg_ram_efficiency,
    max(crm.max_ram_efficiency) AS max_ram_efficiency,
    cr.tags
   FROM (((cloud_resourses cr
     LEFT JOIN spends s ON (((s.cloud_account = cr.account) AND (s.cloud_resource_id = cr.id))))
     LEFT JOIN cloudaccount_k8s_resource_metrics_aggregate crm ON (((crm.cloud_resource_id = cr.id) AND (crm."timestamp" = s.date))))
     LEFT JOIN cloud_resourses intnc ON (((intnc.tenant = cr.tenant) AND (intnc.account = cr.account) AND ((cr.meta ->> 'node'::text) = intnc.name))))
  WHERE ((lower(cr.type) = 'pod'::text) AND ((cr.meta ->> 'node'::text) IS NOT NULL))
  GROUP BY cr.account, cr.tenant, cr.id, cr.name, (cr.meta ->> 'node'::text), ((intnc.meta -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text), ((intnc.meta -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text), s.date;
  
  CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_namespace_aggregate" AS
SELECT
  ca.tenant AS tenant_id,
  ca.id AS account_id,
  cksrpa.namespace_name,
  cksrpa."timestamp",
  sum(cksrpa.pod_cost) AS namespace_cost,
  avg(cksrpa.avg_cpu_used) AS avg_cpu_used,
  max(cksrpa.max_cpu_used) AS max_cpu_used,
  avg(cksrpa.avg_memory_used) AS avg_memory_used,
  max(cksrpa.max_memory_used) AS max_memory_used,
  avg(cksrpa.avg_cpu_request) AS avg_cpu_request,
  max(cksrpa.max_cpu_request) AS max_cpu_request,
  avg(cksrpa.avg_memory_request) AS avg_memory_request,
  max(cksrpa.max_memory_request) AS max_memory_request,
  avg(cksrpa.avg_cpu_efficiency) AS avg_cpu_efficiency,
  max(cksrpa.max_cpu_efficiency) AS max_cpu_efficiency,
  avg(cksrpa.avg_ram_efficiency) AS avg_ram_efficiency,
  max(cksrpa.max_ram_efficiency) AS max_ram_efficiency,
  count(*) AS container_count,
  string_agg(cksrpa.pod_name, ', ' :: text) AS pod_names
FROM
  (
    cloud_accounts ca
    JOIN cloudaccount_k8s_resource_pod_aggregate cksrpa ON ((ca.id = cksrpa.account_id))
  )
GROUP BY
  ca.id,
  ca.tenant,
  cksrpa.namespace_name,
  cksrpa."timestamp";
  
CREATE UNIQUE INDEX cloudaccount_k8s_resource_pod_aggregate_pk ON public.cloudaccount_k8s_resource_pod_aggregate USING btree (id, tenant_id, account_id, "timestamp");
CREATE UNIQUE INDEX cloudaccount_k8s_resource_namespace_aggregate_pk ON public.cloudaccount_k8s_resource_namespace_aggregate USING btree (tenant_id, account_id, namespace_name, "timestamp");

CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_resource_aggregate" AS 
 with cluster_nodes as (
select
	cksna.tenant_id,
	cksna.account_id,
	count(distinct
                case
                    when cksna.is_active is not false then cksna.name
                    else null::text
                end) as node_count,
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
                end) as total_cpu_allocatable,
	sum(
                case
                    when cksna.is_active is not false then cksna.pods_count
                    else null::double precision
                end) as pods_count,
	sum(
                case
                    when cksna.is_active is not false then cksna.pods_count
                    else null::double precision
                end) as sum,
	cksna."timestamp",
	sum(cksna.workload_cost) as node_cost
from
	cloudaccount_k8s_resource_node_aggregate cksna
group by
	cksna.tenant_id,
	cksna.account_id,
	cksna."timestamp"
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
	sum(cn.workload_cost) as mtd_cost,
	cn.account_id as cloud_account_id
from
	cloudaccount_k8s_resource_node_aggregate cn
where
	date_trunc('month'::text, cn."timestamp") = date_trunc('month'::text, CURRENT_DATE::timestamp with time zone)
	and date_trunc('year'::text, cn."timestamp") = date_trunc('year'::text, CURRENT_DATE::timestamp with time zone)
group by
	cn.account_id
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
	cn.node_cost,
	row_number() over (partition by cn.tenant_id,
	cn.account_id
order by
	cn."timestamp" desc) as rn,
	crsa.best_practice_score as best_practice_score,
	crsa.right_sizing_score as right_sizing_score
from
	cluster_nodes cn
left join cluster_pods cp on
	cn.tenant_id = cp.tenant_id
	and cn.account_id = cp.account_id
join cloud_accounts ca on
	cn.tenant_id = ca.tenant
	and cn.account_id = ca.id
left join cloud_spend_mtd csm on
	csm.cloud_account_id = ca.id
left join cloudaccount_k8s_resource_score_aggregate crsa on
	crsa.cloud_account_id = ca.id;

CREATE UNIQUE INDEX cloudaccount_k8s_resource_aggregate_pk ON public.cloudaccount_k8s_resource_aggregate USING btree (tenant_id, account_id, "timestamp");
