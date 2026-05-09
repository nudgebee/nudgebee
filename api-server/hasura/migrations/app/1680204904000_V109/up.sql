
DROP VIEW "public"."cloudaccount_k8s_pod_aggregate";

CREATE OR REPLACE VIEW "public"."cloudaccount_k8s_pod_aggregate" AS 
 SELECT ca.id,
    ca.tenant,
    (cr.tags ->> 'controllerKind'::text) AS workload_type,
    (cr.tags ->> 'controller'::text) AS workload_name,
    (cr.tags ->> 'namespace'::text) AS namespace_name,
    (cr.tags ->> 'pod'::text) AS pod_name,
    (cr.tags ->> 'node' :: text) AS node_name,
    (cr.is_active) AS is_active,
    s.date AS "timestamp",
    sum(s.amount) AS pod_cost,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_cpu_used,
    max(
        CASE
            WHEN (crm.metric = 'cpuCoreUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_used,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_memory_used,
    max(
        CASE
            WHEN (crm.metric = 'ramByteUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_memory_used,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS cpu_request,
    max(
        CASE
            WHEN (crm.metric = 'cpuCoreRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_request,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_memory_request,
    max(
        CASE
            WHEN (crm.metric = 'ramByteRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_memory_request,
    avg(
        CASE
            WHEN (crm.metric = 'cpuEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_cpu_efficiency,
    max(
        CASE
            WHEN (crm.metric = 'cpuEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_efficiency,
    avg(
        CASE
            WHEN (crm.metric = 'ramEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_ram_efficiency,
    max(
        CASE
            WHEN (crm.metric = 'ramEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_ram_efficiency,
    count(*) AS container_count        
   FROM (((cloud_accounts ca
     JOIN cloud_resourses cr ON ((cr.account = ca.id)))
     JOIN spends s ON (((s.cloud_account = ca.id) AND (s.cloud_resource_id = cr.id))))
     JOIN cloud_resource_metrics crm ON ((crm.cloud_resource_id = cr.id)))
     WHERE (ca.account_type = 'kubernetes'::text) 
     	AND ((cr.tags ->> 'controllerKind'::text) IS NOT NULL) 
     	AND (crm.metric = ANY (ARRAY['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text]))
  GROUP BY 
  	ca.id, 
  	ca.tenant, 
  	(cr.tags ->> 'controllerKind'::text), 
  	(cr.tags ->> 'controller'::text), 
  	(cr.tags ->> 'namespace'::text), 
  	(cr.tags ->> 'pod'::text), 
  	(cr.tags ->> 'node' :: text), 
  	cr.is_active,
  	s.date;

DROP VIEW "public"."cloudaccount_k8s_node_aggregate";

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
        END) AS avg_cpu_used,
    max(
        CASE
            WHEN (crm.metric = 'cpuCoreUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_used,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_memory_used,
    max(
        CASE
            WHEN (crm.metric = 'ramByteUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_memory_used,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_cpu_request,
    max(
        CASE
            WHEN (crm.metric = 'cpuCoreRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_request,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_memory_request,
    max(
        CASE
            WHEN (crm.metric = 'ramByteRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_memory_request,
    avg(
        CASE
            WHEN (crm.metric = 'cpuEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_cpu_efficiency,
    max(
        CASE
            WHEN (crm.metric = 'cpuEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_efficiency,
    avg(
        CASE
            WHEN (crm.metric = 'ramEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_ram_efficiency,
    max(
        CASE
            WHEN (crm.metric = 'ramEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_ram_efficiency,
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
     LEFT JOIN cloud_resourses intnc ON (((intnc.tenant = cr.tenant) AND ((cr.tags ->> 'node'::text) = (intnc.meta ->> 'private_dns_name'::text)))))
  WHERE ((ca.account_type = 'kubernetes'::text) AND ((cr.tags ->> 'node'::text) IS NOT NULL) AND (crm.metric = ANY (ARRAY['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text])))
  GROUP BY ca.id, ca.tenant, (cr.tags ->> 'node'::text), (intnc.meta ->> 'spotted'::text), (intnc.meta ->> 'flavor'::text), s.date;

DROP VIEW "public"."cloudaccount_k8s_workload_aggregate";

CREATE OR REPLACE VIEW "public"."cloudaccount_k8s_workload_aggregate" AS 
 SELECT ca.id,
    ca.tenant,
    (cr.tags ->> 'controllerKind'::text) AS workload_type,
    (cr.tags ->> 'controller'::text) AS workload_name,
    (cr.tags ->> 'namespace'::text) AS namespace_name,
    s.date AS "timestamp",
    sum(s.amount) AS workload_cost,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_cpu_used,
    max(
        CASE
            WHEN (crm.metric = 'cpuCoreUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_used,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_memory_used,
    max(
        CASE
            WHEN (crm.metric = 'ramByteUsageAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_memory_used,
    avg(
        CASE
            WHEN (crm.metric = 'cpuCoreRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_cpu_request,
    max(
        CASE
            WHEN (crm.metric = 'cpuCoreRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_request,
    avg(
        CASE
            WHEN (crm.metric = 'ramByteRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_memory_request,
    max(
        CASE
            WHEN (crm.metric = 'ramByteRequestAverage'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_memory_request,
    avg(
        CASE
            WHEN (crm.metric = 'cpuEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_cpu_efficiency,
    max(
        CASE
            WHEN (crm.metric = 'cpuEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_cpu_efficiency,
    avg(
        CASE
            WHEN (crm.metric = 'ramEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS avg_ram_efficiency,
    max(
        CASE
            WHEN (crm.metric = 'ramEfficiency'::text) THEN crm.value
            ELSE NULL::double precision
        END) AS max_ram_efficiency,
    count(DISTINCT
        CASE
            WHEN ((cr.tags ->> 'pod'::text) IS NOT NULL) THEN (cr.tags ->> 'pod'::text)
            ELSE NULL::text
        END) AS pod_count
   FROM (((cloud_accounts ca
     JOIN cloud_resourses cr ON ((cr.account = ca.id)))
     JOIN spends s ON (((s.cloud_account = ca.id) AND (s.cloud_resource_id = cr.id))))
     JOIN cloud_resource_metrics crm ON ((crm.cloud_resource_id = cr.id)))
  WHERE ((ca.account_type = 'kubernetes'::text) AND ((cr.tags ->> 'controllerKind'::text) IS NOT NULL) AND (crm.metric = ANY (ARRAY['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text])))
  GROUP BY ca.id, ca.tenant, (cr.tags ->> 'controllerKind'::text), (cr.tags ->> 'controller'::text), (cr.tags ->> 'namespace'::text), s.date;

DROP VIEW "public"."cloudaccount_k8s_pod_aggregate";

CREATE
OR REPLACE VIEW "public"."cloudaccount_k8s_pod_aggregate" AS
SELECT
  ca.id,
  ca.tenant,
  (cr.tags ->> 'controllerKind' :: text) AS workload_type,
  (cr.tags ->> 'controller' :: text) AS workload_name,
  (cr.tags ->> 'namespace' :: text) AS namespace_name,
  (cr.tags ->> 'pod' :: text) AS pod_name,
  (cr.tags ->> 'node' :: text) AS node_name,
  cr.is_active,
  s.date AS "timestamp",
  sum(s.amount) AS pod_cost,
  avg(
    CASE
      WHEN (crm.metric = 'cpuCoreUsageAverage' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS avg_cpu_used,
  max(
    CASE
      WHEN (crm.metric = 'cpuCoreUsageAverage' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS max_cpu_used,
  avg(
    CASE
      WHEN (crm.metric = 'ramByteUsageAverage' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS avg_memory_used,
  max(
    CASE
      WHEN (crm.metric = 'ramByteUsageAverage' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS max_memory_used,
  avg(
    CASE
      WHEN (crm.metric = 'cpuCoreRequestAverage' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS avg_cpu_request,
  max(
    CASE
      WHEN (crm.metric = 'cpuCoreRequestAverage' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS max_cpu_request,
  avg(
    CASE
      WHEN (crm.metric = 'ramByteRequestAverage' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS avg_memory_request,
  max(
    CASE
      WHEN (crm.metric = 'ramByteRequestAverage' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS max_memory_request,
  avg(
    CASE
      WHEN (crm.metric = 'cpuEfficiency' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS avg_cpu_efficiency,
  max(
    CASE
      WHEN (crm.metric = 'cpuEfficiency' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS max_cpu_efficiency,
  avg(
    CASE
      WHEN (crm.metric = 'ramEfficiency' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS avg_ram_efficiency,
  max(
    CASE
      WHEN (crm.metric = 'ramEfficiency' :: text) THEN crm.value
      ELSE NULL :: double precision
    END
  ) AS max_ram_efficiency,
  count(*) AS container_count
FROM
  (
    (
      (
        cloud_accounts ca
        JOIN cloud_resourses cr ON ((cr.account = ca.id))
      )
      JOIN spends s ON (
        (
          (s.cloud_account = ca.id)
          AND (s.cloud_resource_id = cr.id)
        )
      )
    )
    JOIN cloud_resource_metrics crm ON ((crm.cloud_resource_id = cr.id))
  )
WHERE
  (
    (ca.account_type = 'kubernetes' :: text)
    AND ((cr.tags ->> 'controllerKind' :: text) IS NOT NULL)
    AND (
      crm.metric = ANY (
        ARRAY ['cpuCoreUsageAverage'::text, 'ramByteUsageAverage'::text, 'cpuCoreRequestAverage'::text, 'ramByteRequestAverage'::text]
      )
    )
  )
GROUP BY
  ca.id,
  ca.tenant,
  (cr.tags ->> 'controllerKind' :: text),
  (cr.tags ->> 'controller' :: text),
  (cr.tags ->> 'namespace' :: text),
  (cr.tags ->> 'pod' :: text),
  (cr.tags ->> 'node' :: text),
  cr.is_active,
  s.date;
