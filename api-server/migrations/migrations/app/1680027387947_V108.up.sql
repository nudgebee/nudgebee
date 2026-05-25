
CREATE OR REPLACE VIEW "public"."cloudaccount_k8s_node_aggregate" AS 
 SELECT ca.id,
    ca.tenant,
    (cr.tags ->> 'node'::text) AS node_name,
    s.date AS "timestamp",
    sum(s.amount) AS workload_cost,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_used,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS memory_used,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_request,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS memory_request,
    avg(
        CASE
            WHEN (crm.metric = 'cpuEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_efficiency,
    avg(
        CASE
            WHEN (crm.metric = 'ramEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS ram_efficiency,
    count(DISTINCT
        CASE
            WHEN ((cr.tags ->> 'pod'::text) IS NOT NULL) THEN (cr.tags ->> 'pod'::text)
            ELSE NULL::text
        END) AS pod_count,
    intnc.meta ->> 'spotted' as node_is_spot,
    intnc.meta ->> 'flavor' as node_flavor
   FROM (((cloud_accounts ca
     JOIN cloud_resourses cr ON ((cr.account = ca.id)))
     JOIN spends s ON (((s.cloud_account = ca.id) AND (s.cloud_resource_id = cr.id))))
     JOIN cloud_resource_metrics crm ON ((crm.cloud_resource_id = cr.id)))
     LEFT JOIN cloud_resourses intnc on cr.tags ->> 'node' = intnc.meta ->> 'private_dns_name'
  WHERE ((ca.account_type = 'kubernetes'::text) AND ((cr.tags ->> 'node'::text) IS NOT NULL) AND (crm.metric = ANY (ARRAY['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text])))
  GROUP BY ca.id, ca.tenant, (cr.tags ->> 'node'::text), intnc.meta ->> 'spotted', intnc.meta ->> 'flavor', s.date;

CREATE OR REPLACE VIEW "public"."cloudaccount_k8s_node_aggregate" AS 
 SELECT ca.id,
    ca.tenant,
    (cr.tags ->> 'node'::text) AS node_name,
    s.date AS "timestamp",
    sum(s.amount) AS workload_cost,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_used,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS memory_used,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_request,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS memory_request,
    avg(
        CASE
            WHEN (crm.metric = 'cpuEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_efficiency,
    avg(
        CASE
            WHEN (crm.metric = 'ramEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS ram_efficiency,
    count(DISTINCT
        CASE
            WHEN ((cr.tags ->> 'pod'::text) IS NOT NULL) THEN (cr.tags ->> 'pod'::text)
            ELSE NULL::text
        END) AS pod_count,
    (intnc.meta ->> 'spotted'::text) AS node_is_spot,
    (intnc.meta ->> 'flavor'::text) AS node_flavor
   FROM ((((cloud_accounts ca
     JOIN cloud_resourses cr ON ((cr.account = ca.id)))
     JOIN spends s ON (((s.cloud_account = ca.id) AND (s.cloud_resource_id = cr.id))))
     JOIN cloud_resource_metrics crm ON ((crm.cloud_resource_id = cr.id)))
     LEFT JOIN cloud_resourses intnc ON (intnc.account = cr.account and ((cr.tags ->> 'node'::text) = (intnc.meta ->> 'private_dns_name'::text))))
  WHERE ((ca.account_type = 'kubernetes'::text) AND ((cr.tags ->> 'node'::text) IS NOT NULL) AND (crm.metric = ANY (ARRAY['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text])))
  GROUP BY ca.id, ca.tenant, (cr.tags ->> 'node'::text), (intnc.meta ->> 'spotted'::text), (intnc.meta ->> 'flavor'::text), s.date;

CREATE OR REPLACE VIEW "public"."cloudaccount_k8s_node_aggregate" AS 
 SELECT ca.id,
    ca.tenant,
    (cr.tags ->> 'node'::text) AS node_name,
    s.date AS "timestamp",
    sum(s.amount) AS workload_cost,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_used,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS memory_used,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_request,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS memory_request,
    avg(
        CASE
            WHEN (crm.metric = 'cpuEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_efficiency,
    avg(
        CASE
            WHEN (crm.metric = 'ramEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS ram_efficiency,
    count(DISTINCT
        CASE
            WHEN ((cr.tags ->> 'pod'::text) IS NOT NULL) THEN (cr.tags ->> 'pod'::text)
            ELSE NULL::text
        END) AS pod_count,
    (intnc.meta ->> 'spotted'::text) AS node_is_spot,
    (intnc.meta ->> 'flavor'::text) AS node_flavor
   FROM ((((cloud_accounts ca
     JOIN cloud_resourses cr ON ((cr.account = ca.id)))
     JOIN spends s ON (((s.cloud_account = ca.id) AND (s.cloud_resource_id = cr.id))))
     JOIN cloud_resource_metrics crm ON ((crm.cloud_resource_id = cr.id)))
     LEFT JOIN cloud_resourses intnc ON (intnc.tenant = cr.tenant and ((cr.tags ->> 'node'::text) = (intnc.meta ->> 'private_dns_name'::text))))
  WHERE ((ca.account_type = 'kubernetes'::text) AND ((cr.tags ->> 'node'::text) IS NOT NULL) AND (crm.metric = ANY (ARRAY['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text])))
  GROUP BY ca.id, ca.tenant, (cr.tags ->> 'node'::text), (intnc.meta ->> 'spotted'::text), (intnc.meta ->> 'flavor'::text), s.date;
