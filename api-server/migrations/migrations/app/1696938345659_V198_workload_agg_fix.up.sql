-- public.cloudaccount_k8s_resource_workload_aggregate source
DROP MATERIALIZED VIEW public.cloudaccount_k8s_resource_workload_aggregate;

CREATE MATERIALIZED VIEW public.cloudaccount_k8s_resource_workload_aggregate
TABLESPACE pg_default
AS SELECT ca.tenant AS tenant_id,
    ca.id AS account_id,
    cr2.external_resource_id AS external_workload_id,
    cr.meta ->> 'namespace'::text AS namespace_name,
    cr.meta ->> 'controllerKind'::text AS workload_type,
    cr.meta ->> 'controller'::text AS workload_name,
    s.date AS "timestamp",
    cr2.is_active,
    max(cr2.meta ->> 'total_pods'::text) AS total_pods,
    max(cr2.meta ->> 'ready_pods'::text) AS ready_pods,
    max(cr.first_seen) AS workload_creation_time,
    sum(s.amount) AS workload_cost,
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
    max(crm.max_ram_efficiency) AS max_ram_efficiency
   FROM cloud_accounts ca
     JOIN cloud_resourses cr ON cr.account = ca.id
     LEFT JOIN spends s ON s.cloud_account = ca.id AND s.cloud_resource_id = cr.id
     LEFT JOIN cloudaccount_k8s_resource_metrics_aggregate crm ON crm.cloud_resource_id = cr.id AND s.date = crm."timestamp"
     JOIN cloud_resourses cr2 ON cr.account = cr2.account AND (cr.meta ->> 'controller'::text) = cr2.name AND (cr.meta ->> 'controllerKind'::text) = cr2.type AND (cr.meta ->> 'namespace'::text) = (cr2.meta ->> 'namespace'::text)
  WHERE ca.account_type = 'kubernetes'::text AND lower(cr.type) = 'pod'::text AND cr.is_active IS NOT FALSE AND (cr.meta ->> 'controllerKind'::text) IS NOT NULL
  GROUP BY cr2.id, ca.id, ca.tenant, (cr.meta ->> 'controllerKind'::text), (cr.meta ->> 'controller'::text), (cr.meta ->> 'namespace'::text), (cr2.meta ->> 'total_pods'::text), (cr2.meta ->> 'ready_pods'::text), cr2.is_active, s.date
WITH DATA;

-- View indexes:
CREATE UNIQUE INDEX cloudaccount_k8s_resource_workload_aggregate_pk ON public.cloudaccount_k8s_resource_workload_aggregate USING btree (external_workload_id, tenant_id, account_id, "timestamp");
