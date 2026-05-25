DROP MATERIALIZED VIEW public.cloudaccount_k8s_resource_aggregate;
DROP MATERIALIZED VIEW public.cloudaccount_k8s_resource_namespace_aggregate;
DROP MATERIALIZED VIEW public.cloudaccount_k8s_resource_node_aggregate;
DROP MATERIALIZED VIEW public.cloudaccount_k8s_resource_workload_aggregate;
DROP MATERIALIZED VIEW public.cloudaccount_k8s_resource_pod_aggregate;
DROP MATERIALIZED VIEW public.cloudaccount_k8s_resource_metrics_aggregate;


-- public.cloudaccount_k8s_resource_metrics_aggregate source
CREATE MATERIALIZED VIEW public.cloudaccount_k8s_resource_metrics_aggregate
TABLESPACE pg_default
AS SELECT cr.id AS cloud_resource_id,
    cr.tenant,
    cr.account,
    cr.type,
    crm."timestamp",
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
    avg(
        CASE
            WHEN crm.metric = 'totalEfficiency'::text THEN crm.value
            ELSE NULL::double precision
        END) AS avg_total_efficiency,
    max(
        CASE
            WHEN crm.metric = 'totalEfficiency'::text THEN crm.value
            ELSE NULL::double precision
        END) AS max_total_efficiency,
    avg(
        CASE
            WHEN crm.metric = 'memory_capacity'::text THEN crm.value
            ELSE NULL::double precision
        END) AS memory_capacity,
    avg(
        CASE
            WHEN crm.metric = 'memory_allocatable'::text THEN crm.value
            ELSE NULL::double precision
        END) AS memory_allocatable,
    avg(
        CASE
            WHEN crm.metric = 'memory_allocated'::text THEN crm.value
            ELSE NULL::double precision
        END) AS memory_allocated,
    avg(
        CASE
            WHEN crm.metric = 'cpu_allocatable'::text THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_allocatable,
    avg(
        CASE
            WHEN crm.metric = 'cpu_capacity'::text THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_capacity,
    avg(
        CASE
            WHEN crm.metric = 'cpu_allocated'::text THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_allocated,
    avg(
        CASE
            WHEN crm.metric = 'pods_count'::text THEN crm.value
            ELSE NULL::double precision
        END) AS pods_count,
    sum(
        CASE
            WHEN crm.metric = 'cpuCost'::text THEN crm.value
            ELSE NULL::double precision
        END) AS total_cpu_cost,
    sum(
        CASE
            WHEN crm.metric = 'ramCost'::text THEN crm.value
            ELSE NULL::double precision
        END) AS total_ram_cost,
    sum(
        CASE
            WHEN crm.metric = 'gpuCost'::text THEN crm.value
            ELSE NULL::double precision
        END) AS total_gpu_cost
   FROM cloud_accounts ca
     JOIN cloud_resourses cr ON ca.id = cr.account
     LEFT JOIN cloud_resource_metrics crm ON crm.cloud_resource_id = cr.id
  WHERE ca.cloud_provider = 'K8s'::text AND (crm.metric = ANY (ARRAY['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text, 'memory_capacity'::text, 'memory_allocatable'::text, 'memory_allocated'::text, 'cpu_capacity'::text, 'cpu_allocatable'::text, 'cpu_allocated'::text, 'pods_count'::text, 'totalEfficiency'::text, 'ramEfficiency'::text, 'cpuEfficiency'::text, 'cpuCost'::text, 'ramCost'::text, 'gpuCost'::text]))
  GROUP BY cr.id, cr.account, cr.tenant, crm."timestamp"
WITH DATA;

-- View indexes:
CREATE UNIQUE INDEX cloudaccount_k8s_resource_metric_aggregate_pk ON public.cloudaccount_k8s_resource_metrics_aggregate USING btree (cloud_resource_id, tenant, account, "timestamp");

-- public.cloudaccount_k8s_resource_pod_aggregate source

CREATE MATERIALIZED VIEW public.cloudaccount_k8s_resource_pod_aggregate
TABLESPACE pg_default
AS SELECT cr.tenant AS tenant_id,
    cr.account AS account_id,
    cr.id,
    cr.name AS pod_name,
    cr.is_active,
    s.date AS "timestamp",
    cr.meta ->> 'controller'::text AS workload_name,
    cr.meta ->> 'controllerKind'::text AS workload_type,
    cr.meta ->> 'namespace'::text AS namespace_name,
    cr.meta ->> 'status'::text AS status,
    cr.meta ->> 'restart_count'::text AS restart_count,
    cr.first_seen AS creation_time,
    cr.meta ->> 'node'::text AS node_name,
        CASE
            WHEN ((intnc.meta -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text) IS NOT NULL THEN (intnc.meta -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text
            ELSE 'on-demand'::text
        END AS node_type,
    (intnc.meta -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text AS node_flavor,
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
    max(crm.avg_ram_efficiency) AS max_ram_efficiency,
    max(crm.max_total_efficiency) AS max_total_efficiency,
    cr.tags,
    sum(crm.total_cpu_cost) AS total_cpu_cost,
    sum(crm.total_ram_cost) AS total_ram_cost,
    sum(crm.total_gpu_cost) AS total_gpu_cost
   FROM cloud_resourses cr
     LEFT JOIN spends s ON s.cloud_account = cr.account AND s.cloud_resource_id = cr.id
     LEFT JOIN cloudaccount_k8s_resource_metrics_aggregate crm ON crm.cloud_resource_id = cr.id AND crm."timestamp" = s.date
     LEFT JOIN cloud_resourses intnc ON intnc.tenant = cr.tenant AND intnc.account = cr.account AND (cr.meta ->> 'node'::text) = intnc.name
  WHERE lower(cr.type) = 'pod'::text AND (cr.meta ->> 'node'::text) IS NOT NULL
  GROUP BY cr.account, cr.tenant, cr.id, cr.name, (cr.meta ->> 'node'::text), ((intnc.meta -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text), ((intnc.meta -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text), s.date
WITH DATA;

-- View indexes:
CREATE UNIQUE INDEX cloudaccount_k8s_resource_pod_aggregate_pk ON public.cloudaccount_k8s_resource_pod_aggregate USING btree (id, tenant_id, account_id, "timestamp");



-- public.cloudaccount_k8s_resource_workload_aggregate source

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
    max(crm.max_ram_efficiency) AS max_ram_efficiency,
    max(crm.max_total_efficiency) AS max_total_efficiency
   FROM cloud_accounts ca
     JOIN cloud_resourses cr ON cr.account = ca.id
     LEFT JOIN spends s ON s.cloud_account = ca.id AND s.cloud_resource_id = cr.id
     LEFT JOIN cloudaccount_k8s_resource_metrics_aggregate crm ON crm.cloud_resource_id = cr.id AND s.date = crm."timestamp"
     JOIN cloud_resourses cr2 ON cr.account = cr2.account AND (cr.meta ->> 'controller'::text) = cr2.name AND (cr.meta ->> 'controllerKind'::text) = cr2.type AND (cr.meta ->> 'namespace'::text) = (cr2.meta ->> 'namespace'::text)
  WHERE ca.cloud_provider = 'K8s'::text AND lower(cr.type) = 'pod'::text AND cr.is_active IS NOT FALSE AND (cr.meta ->> 'controllerKind'::text) IS NOT NULL
  GROUP BY cr2.id, ca.id, ca.tenant, (cr.meta ->> 'controllerKind'::text), (cr.meta ->> 'controller'::text), (cr.meta ->> 'namespace'::text), (cr2.meta ->> 'total_pods'::text), (cr2.meta ->> 'ready_pods'::text), cr2.is_active, s.date
WITH DATA;

-- View indexes:
CREATE UNIQUE INDEX cloudaccount_k8s_resource_workload_aggregate_pk ON public.cloudaccount_k8s_resource_workload_aggregate USING btree (external_workload_id, tenant_id, account_id, "timestamp");


-- public.cloudaccount_k8s_resource_node_aggregate source

CREATE MATERIALIZED VIEW public.cloudaccount_k8s_resource_node_aggregate
TABLESPACE pg_default
AS SELECT cr.tenant AS tenant_id,
    cr.account AS account_id,
    cr.name,
    cr.id,
    cr.is_active,
    s.date AS "timestamp",
    min(cr.first_seen) AS node_creation_time,
    cr.meta ->> 'conditions'::text AS conditions,
        CASE
            WHEN (((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text) IS NOT NULL THEN ((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text
            ELSE 'on-demand'::text
        END AS node_type,
    ((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text AS node_flavor,
    ((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'topology.kubernetes.io/region'::text AS node_region,
    ((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'topology.kubernetes.io/zone'::text AS node_zone,
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
    max(crm.max_ram_efficiency) AS max_ram_efficiency,
    max(crm.max_total_efficiency) AS max_total_efficiency,
    max(cr.meta ->> 'cpu_allocated'::text)::double precision AS cpu_allocated,
    max(cr.meta ->> 'memory_capacity'::text)::double precision AS memory_capacity,
    max(cr.meta ->> 'cpu_capacity'::text)::double precision AS cpu_capacity,
    max(cr.meta ->> 'memory_allocatable'::text)::double precision AS memory_allocatable,
    max(cr.meta ->> 'cpu_allocatable'::text)::double precision AS cpu_allocatable,
    max(cr.meta ->> 'memory_allocated'::text)::double precision AS memory_allocated,
    max(cr.meta ->> 'pods_count'::text)::integer AS pods_count,
    max(crd.resource_cost) AS resource_cost_per_hour,
    sum(crm.total_cpu_cost) AS total_cpu_cost,
    sum(crm.total_ram_cost) AS total_ram_cost,
    sum(crm.total_gpu_cost) AS total_gpu_cost,
    sum(s.amount) - (sum(crm.total_gpu_cost) + sum(crm.total_cpu_cost) + sum(crm.total_ram_cost)) AS total_idle_cost
   FROM cloud_resourses cr
     JOIN spends s ON s.cloud_account = cr.account AND s.cloud_resource_id = cr.id
     LEFT JOIN cloudaccount_k8s_resource_metrics_aggregate crm ON crm.cloud_resource_id = cr.id AND s.date = crm."timestamp"
     LEFT JOIN cloud_resource_details crd ON crd.resource_type = (((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text) AND crd.resource_region = (((cr.meta -> 'node_info'::text) -> 'labels'::text) ->> 'topology.kubernetes.io/region'::text)
  WHERE lower(cr.type) = 'node'::text
  GROUP BY cr.id, cr.account, cr.tenant, cr.name, s.date, ((cr.tags -> 'labels'::text) ->> 'node.kubernetes.io/instance-type'::text), ((cr.tags -> 'labels'::text) ->> 'karpenter.sh/capacity-type'::text), (cr.meta ->> 'conditions'::text)
WITH DATA;

-- View indexes:
CREATE UNIQUE INDEX cloudaccount_k8s_resource_node_aggregate_pk ON public.cloudaccount_k8s_resource_node_aggregate USING btree (id, tenant_id, account_id, "timestamp");


-- public.cloudaccount_k8s_resource_namespace_aggregate source

CREATE MATERIALIZED VIEW public.cloudaccount_k8s_resource_namespace_aggregate
TABLESPACE pg_default
AS SELECT ca.tenant AS tenant_id,
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
    max(cksrpa.max_total_efficiency) AS max_total_efficiency,
    count(*) AS container_count,
    string_agg(cksrpa.pod_name, ', '::text) AS pod_names,
    sum(cksrpa.pod_cost) - (sum(cksrpa.total_gpu_cost) + sum(cksrpa.total_cpu_cost) + sum(cksrpa.total_ram_cost)) AS total_idle_cost
   FROM cloud_accounts ca
     JOIN cloudaccount_k8s_resource_pod_aggregate cksrpa ON ca.id = cksrpa.account_id
  GROUP BY ca.id, ca.tenant, cksrpa.namespace_name, cksrpa."timestamp"
WITH DATA;

-- View indexes:
CREATE UNIQUE INDEX cloudaccount_k8s_resource_namespace_aggregate_pk ON public.cloudaccount_k8s_resource_namespace_aggregate USING btree (tenant_id, account_id, namespace_name, "timestamp");

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
                    WHEN lower(cksna.node_type) = 'on-demand'::text AND cksna.is_active IS NOT FALSE THEN cksna.name
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
                    WHEN cksna.is_active IS NOT FALSE THEN cksna.pods_count::double precision
                    ELSE NULL::double precision
                END) AS pods_count,
            cksna."timestamp",
            sum(cksna.workload_cost) AS node_cost,
            sum(cksna.total_idle_cost) AS total_idle_cost,
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
            sum(ckspa.pod_cost) - (sum(ckspa.total_gpu_cost) + sum(ckspa.total_cpu_cost) + sum(ckspa.total_ram_cost)) AS total_idle_cost
           FROM cloudaccount_k8s_resource_pod_aggregate ckspa
          GROUP BY ckspa.tenant_id, ckspa.account_id
        ), cloud_spend_mtd AS (
         SELECT sum(cn_1.workload_cost) AS mtd_cost,
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
    csm.mtd_cost,
    cn."timestamp",
    cn.node_cost,
    row_number() OVER (PARTITION BY cn.tenant_id, cn.account_id ORDER BY cn."timestamp" DESC) AS rn,
    crsa.best_practice_score,
    crsa.right_sizing_score,
    cn.total_idle_cost,
    cn.total_efficiency
   FROM cluster_nodes cn
     LEFT JOIN cluster_pods cp ON cn.tenant_id = cp.tenant_id AND cn.account_id = cp.account_id
     JOIN cloud_accounts ca ON cn.tenant_id = ca.tenant AND cn.account_id = ca.id
     LEFT JOIN cloud_spend_mtd csm ON csm.cloud_account_id = ca.id
     LEFT JOIN cloudaccount_k8s_resource_score_aggregate crsa ON crsa.cloud_account_id = ca.id
WITH DATA;

-- View indexes:
CREATE UNIQUE INDEX cloudaccount_k8s_resource_aggregate_pk ON public.cloudaccount_k8s_resource_aggregate USING btree (tenant_id, account_id, "timestamp");
