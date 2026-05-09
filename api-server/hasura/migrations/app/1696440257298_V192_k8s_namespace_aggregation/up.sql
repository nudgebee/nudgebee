CREATE MATERIALIZED VIEW "public"."cloudaccount_k8s_namespace_aggregate" AS 
SELECT ca.tenant AS tenant_id,
    ca.id AS account_id,
    cr.tags ->> 'namespace'::text AS namespace_name,
    s.date AS "timestamp",
    sum(s.amount) AS namespace_cost,
    avg(
        CASE
            WHEN crm.metric = 'cpuCoreUsageAverage'::text THEN crm.value
            ELSE NULL::double precision
        END) AS avg_cpu_used,
    max(
        CASE
            WHEN crm.metric = 'cpuCoreUsageAverage'::text THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_used,
    avg(
        CASE
            WHEN crm.metric = 'ramByteUsageAverage'::text THEN crm.value
            ELSE NULL::double precision
        END) AS avg_memory_used,
    max(
        CASE
            WHEN crm.metric = 'ramByteUsageAverage'::text THEN crm.value
            ELSE NULL::double precision
        END) AS max_memory_used,
    avg(
        CASE
            WHEN crm.metric = 'cpuCoreRequestAverage'::text THEN crm.value
            ELSE NULL::double precision
        END) AS avg_cpu_request,
    max(
        CASE
            WHEN crm.metric = 'cpuCoreRequestAverage'::text THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_request,
    avg(
        CASE
            WHEN crm.metric = 'ramByteRequestAverage'::text THEN crm.value
            ELSE NULL::double precision
        END) AS avg_memory_request,
    max(
        CASE
            WHEN crm.metric = 'ramByteRequestAverage'::text THEN crm.value
            ELSE NULL::double precision
        END) AS max_memory_request,
    avg(
        CASE
            WHEN crm.metric = 'cpuEfficiency'::text THEN crm.value
            ELSE NULL::double precision
        END) AS avg_cpu_efficiency,
    max(
        CASE
            WHEN crm.metric = 'cpuEfficiency'::text THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_efficiency,
    avg(
        CASE
            WHEN crm.metric = 'ramEfficiency'::text THEN crm.value
            ELSE NULL::double precision
        END) AS avg_ram_efficiency,
    max(
        CASE
            WHEN crm.metric = 'ramEfficiency'::text THEN crm.value
            ELSE NULL::double precision
        END) AS max_ram_efficiency,
    count(*) AS container_count
   FROM cloud_accounts ca
     JOIN cloud_resourses cr ON cr.account = ca.id
     JOIN spends s ON s.cloud_account = ca.id AND s.cloud_resource_id = cr.id
     JOIN cloud_resource_metrics crm ON crm.cloud_resource_id = cr.id
  WHERE ca.account_type = 'kubernetes'::text AND lower(cr.type) = 'pod'::text AND cr.is_active IS NOT FALSE AND (cr.tags ->> 'controllerKind'::text) IS NOT NULL AND (crm.metric = ANY (ARRAY['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text]))
  GROUP BY ca.id, ca.tenant, (cr.tags ->> 'namespace'::text), s.date
  order by s.date;
  
  CREATE UNIQUE INDEX cloudaccount_k8s_namespace_aggregate_pk ON public.cloudaccount_k8s_namespace_aggregate USING btree (tenant_id, account_id, namespace_name, "timestamp");
