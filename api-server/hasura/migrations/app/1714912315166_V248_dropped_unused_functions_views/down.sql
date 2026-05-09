
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."cloud_services_aggregate";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."cloud_resource_metrics_daily_aggregate";


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."k8s_pod_groupings_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."auto_playbook_groupings_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."cloud_resource_metrics_groupings_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."event_groupings_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."recommendation_groupings_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."spend_groupings_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP VIEW "public"."ticket_groupings_type";

CREATE OR REPLACE FUNCTION public.k8s_pod_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, date_unit text DEFAULT 'day'::text, "limit" integer DEFAULT 100, "offset" integer DEFAULT 0, sort_by text DEFAULT 'timestamp'::text, sort_order text DEFAULT 'asc'::text, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF k8s_pod_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN crm.tenant_id
      ELSE NULL
    END
  ) AS tenant_id,
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN crm.cloud_account_id
      ELSE NULL
    END
  ) AS account_id,
  (
    CASE
      WHEN 'workload_type' = ANY(group_by) THEN crm.tags ->> 'controllerKind'
      ELSE NULL
    END
  ) AS workload_type,
  (
    CASE
      WHEN 'workload_name' = ANY(group_by) THEN crm.tags ->> 'controller'
      ELSE NULL
    END
  ) AS workload_name,
  (
    CASE
      WHEN 'namespace_name' = ANY(group_by) THEN crm.tags ->> 'namespace'
      ELSE NULL
    END
  ) AS namespace_name,
  (
    CASE
      WHEN 'pod_name' = ANY(group_by) THEN crm.tags ->> 'name'
      ELSE NULL
    END
  ) AS pod_name,
  (
    CASE
      WHEN 'node_name' = ANY(group_by) THEN crm.tags ->> 'node'
      ELSE NULL
    END
  ) AS node_name,
  (
    CASE
      WHEN 'timestamp' = ANY(group_by) THEN date_trunc(date_unit, crm."timestamp")
      ELSE NULL::timestamp
    END
  ) AS timestamp,
  sum(case when crm.metric = 'totalCost' then crm.value end) AS cost,
  avg(case when crm.metric = 'totalEfficiency' then crm.value end) AS avg_efficiency,
  max(case when crm.metric = 'totalEfficiency' then crm.value end) AS max_efficiency,
  avg(case when crm.metric = 'cpuCoreUsageAverage' then crm.value end) AS avg_cpu_used,
  max(case when crm.metric = 'cpuCoreUsageAverage' then crm.value end) AS max_cpu_used,
  avg(case when crm.metric = 'ramByteUsageAverage' then crm.value end) AS avg_memory_used,
  max(case when crm.metric = 'ramByteUsageAverage' then crm.value end) AS max_memory_used,
  avg(case when crm.metric = 'cpuCoreRequestAverage' then crm.value end) AS avg_cpu_request,
  max(case when crm.metric = 'cpuCoreRequestAverage' then crm.value end) AS max_cpu_request,
  avg(case when crm.metric = 'ramByteRequestAverage' then crm.value end) AS avg_memory_request,
  max(case when crm.metric = 'ramByteRequestAverage' then crm.value end) AS max_memory_request,
  avg(case when crm.metric = 'cpuEfficiency' then crm.value end) AS avg_cpu_efficiency,
  max(case when crm.metric = 'cpuEfficiency' then crm.value end) AS max_cpu_efficiency,
  avg(case when crm.metric = 'ramEfficiency' then crm.value end) AS avg_ram_efficiency,
  max(case when crm.metric = 'ramEfficiency' then crm.value end) AS max_ram_efficiency,
  sum(case when crm.metric = 'networkReceiveBytes' then crm.value end) AS sum_ingress,
  sum(case when crm.metric = 'networkTransferBytes' then crm.value end) AS sum_egress
from cloud_resource_metrics crm
where
  (
    "hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL
    OR (
      crm."tenant_id" = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid
    )
  )
  AND (
    "where" #>> '{account_id,_eq}' IS NULL
    OR (
      crm."cloud_account_id" = ("where" #>> '{account_id,_eq}') :: uuid
    )
  )
  AND crm.metric IN (
    'totalCost',
    'totalEfficiency',
    'cpuCoreUsageAverage',
    'ramByteUsageAverage',
    'cpuCoreRequestAverage',
    'ramByteRequestAverage',
    'cpuEfficiency',
    'ramEfficiency',
    'networkReceiveBytes',
    'networkTransferBytes'
  )
  AND (
    "where" #>> '{workload_type,_eq}' IS NULL
    OR (
      crm.tags ->> 'controllerKind' = ("where" #>> '{workload_type,_eq}')
    )
  )
  AND (
    "where" #>> '{workload_type,_in}' IS NULL
    OR (
      ("where" #>> '{workload_type,_in}')::jsonb ? (crm.tags ->> 'workload_type')::text
    )
  )
  AND (
    "where" #>> '{workload_name,_eq}' IS NULL
    OR (
      crm.tags ->> 'controller' = ("where" #>> '{workload_name,_eq}')
    )
  )
  AND (
    "where" #>> '{workload_fqdn,_eq}' IS NULL
    OR (
      crm.tags ->> 'workload_fqdn' = ("where" #>> '{workload_fqdn,_eq}')
    )
  )    
  AND (
    "where" #>> '{workload_fqdn,_in}' IS NULL
    OR (
      ("where" #>> '{workload_fqdn,_in}')::jsonb ? (crm.tags ->> 'workload_fqdn')::text
    )
  )
  AND (
    "where" #>> '{namespace_name,_eq}' IS NULL
    OR (
      crm.tags ->> 'namespace' = ("where" #>> '{namespace_name,_eq}')
    )
  )
  AND (
    "where" #>> '{namespace_name,_in}' IS NULL
    OR (
      ("where" #>> '{namespace_name,_in}')::jsonb ? (crm.tags ->> 'namespace')::text
    )
  )
  AND (
    "where" #>> '{pod_name,_eq}' IS NULL
    OR (
      crm.tags ->> 'name' = ("where" #>> '{pod_name,_eq}')
    )
  ) 
  AND (
    "where" #>> '{pod_fqdn,_eq}' IS NULL
    OR (
      crm.tags ->> 'pod_fqdn' = ("where" #>> '{pod_fqdn,_eq}')
    )
  )    
  AND (
    "where" #>> '{pod_fqdn,_in}' IS NULL
    OR (
      ("where" #>> '{pod_fqdn,_in}')::jsonb ? (crm.tags ->> 'pod_fqdn')::text
    )
  )
  AND (
    "where" #>> '{node_name,_eq}' IS NULL
    OR (
      crm.tags ->> 'node' = ("where" #>> '{node_name,_eq}')
    )
  )  
  AND (
    "where" #>> '{node_name,_in}' IS NULL
    OR (
      ("where" #>> '{node_name,_in}')::jsonb ? (crm.tags ->> 'node')::text
    )
  )
  AND (
    "where" #>> '{timestamp,_eq}' IS NULL
    OR (
      crm."timestamp" = ("where" #>> '{timestamp,_eq}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_gt}' IS NULL
    OR (
      crm."timestamp" > ("where" #>> '{timestamp,_gt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_lt}' IS NULL
    OR (
      crm."timestamp" < ("where" #>> '{timestamp,_lt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_le}' IS NULL
    OR (
      crm."timestamp" <= ("where" #>> '{timestamp,_le}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_ge}' IS NULL
    OR (
      crm."timestamp" >= ("where" #>> '{timestamp,_ge}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,le}' IS NULL
    OR (
      crm."timestamp" <= ("where" #>> '{timestamp,_between,le}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,lt}' IS NULL
    OR (
      crm."timestamp" < ("where" #>> '{timestamp,_between,lt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,gt}' IS NULL
    OR (
      crm."timestamp" > ("where" #>> '{timestamp,_between,gt}') :: timestamp
    )
  )
  AND (
    "where" #>> '{timestamp,_between,ge}' IS NULL
    OR (
      crm."timestamp" >= ("where" #>> '{timestamp,_between,ge}') :: timestamp
    )
  )
group BY
  (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN crm.tenant_id
    END
  ),
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN crm.cloud_account_id
    END
  ),
  (
    CASE
      WHEN 'workload_type' = ANY(group_by) THEN crm.tags ->> 'controllerKind'
    END
  ),
  (
    CASE
      WHEN 'workload_name' = ANY(group_by) THEN crm.tags ->> 'controller'
    END
  ),
  (
    CASE
      WHEN 'namespace_name' = ANY(group_by) THEN crm.tags ->> 'namespace'
    END
  ),
  (
    CASE
      WHEN 'pod_name' = ANY(group_by) THEN crm.tags ->> 'name'
    END
  ),
  (
    CASE
      WHEN 'node_name' = ANY(group_by) THEN crm.tags ->> 'node'
    END
  ),
  (
    CASE
      WHEN 'timestamp' = ANY(group_by) THEN date_trunc(date_unit, crm."timestamp")
    END
  )
order by "timestamp" desc
limit "limit" offset "offset"  
$function$;

CREATE OR REPLACE FUNCTION public.ticket_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF ticket_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
     (CASE WHEN 'tenant_id' = ANY(group_by) THEN t.tenant ELSE null end ) AS tenant_id,
     (CASE WHEN 'config_name' = ANY(group_by) THEN jc.name ELSE NULL END) AS config_name, 
     (CASE WHEN 'config_id' = ANY(group_by) THEN jc.id ELSE NULL END) AS config_id, 
     (CASE WHEN 'ticket_type' = ANY(group_by) THEN t.ticket_type ELSE NULL END) AS ticket_type,
     (CASE WHEN 'reference_id' = ANY(group_by) THEN t.reference_id ELSE NULL END) AS reference_id,
     (CASE WHEN 'status' = ANY(group_by) THEN t.status ELSE NULL END) AS status,
     (CASE WHEN 'assignee' = ANY(group_by) THEN t.assignee ELSE NULL END) AS assignee,
     (CASE WHEN 'created_by' = ANY(group_by) THEN t.created_by ELSE NULL END) AS created_by,
     (CASE WHEN 'severity' = ANY(group_by) THEN t.severity ELSE NULL END) AS severity,
     (CASE WHEN 'created_at' = ANY(group_by) THEN t.created_at::date ELSE NULL END) AS created_at,
     COUNT(t.*) AS count
FROM tickets t 
LEFT JOIN jira_configurations jc ON t.configuration_id = jc.id
WHERE 
    ("hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL OR ( t.tenant = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid))
    AND ("where" #>> '{config_name,_eq}' IS null OR (jc.name = ("where" #>> '{config_name,_eq}')))
    AND ("where" #>> '{config_id,_eq}' IS null OR (jc.id = ("where" #>> '{config_id,_eq}')::uuid))
    AND ("where" #>> '{ticket_type,_eq}' IS null OR (t.ticket_type = ("where" #>> '{ticket_type,_eq}')))
    AND ("where" #>> '{reference_id,_eq}' IS null OR (t.reference_id = ("where" #>> '{reference_id,_eq}')::uuid))
    AND ("where" #>> '{status,_eq}' IS null OR (t.status = ("where" #>> '{status,_eq}')))
    AND ("where" #>> '{assignee,_eq}' IS null OR (t.assignee = ("where" #>> '{assignee,_eq}')))
    AND ("where" #>> '{created_by,_eq}' IS null OR (t.created_by = ("where" #>> '{created_by,_eq}')::uuid))
    AND ("where" #>> '{severity,_eq}' IS null OR (t.severity = ("where" #>> '{severity,_eq}')))
    AND ("where" #>> '{created_at,_eq}' IS null OR (t.created_at::date = ("where" #>> '{created_at,_eq}')::date))
    AND ("where" #>> '{created_at,_gt}' IS null OR (t.created_at::date > ("where" #>> '{created_at,_gt}')::date))
    AND ("where" #>> '{created_at,_lt}' IS null OR (t.created_at::date < ("where" #>> '{created_at,_lt}')::date))
    AND ("where" #>> '{created_at,_ge}' IS null OR (t.created_at::date >= ("where" #>> '{created_at,_ge}')::date))
    AND ("where" #>> '{created_at,_le}' IS null OR (t.created_at::date <= ("where" #>> '{created_at,_le}')::date))
GROUP BY
    (CASE WHEN 'tenant_id' = ANY(group_by) THEN t.tenant END),
    (CASE WHEN 'config_name' = ANY(group_by) THEN jc.name END),
    (CASE WHEN 'config_id' = ANY(group_by) THEN jc.id END),
    (CASE WHEN 'ticket_type' = ANY(group_by) THEN t.ticket_type END),
    (CASE WHEN 'reference_id' = ANY(group_by) THEN t.reference_id END),
    (CASE WHEN 'status' = ANY(group_by) THEN t.status END),
    (CASE WHEN 'assignee' = ANY(group_by) THEN t.assignee END),
    (CASE WHEN 'created_by' = ANY(group_by) THEN t.created_by END),
    (CASE WHEN 'severity' = ANY(group_by) THEN t.severity END),
    (CASE WHEN 'created_at' = ANY(group_by) THEN t.created_at::date END)
$function$;

CREATE OR REPLACE FUNCTION public.search_auto_playbook(playbook_account_id text, playbook_status text DEFAULT NULL::text, playbook_name text DEFAULT NULL::text, type_filter text DEFAULT NULL::text, name_filter text DEFAULT NULL::text, limit_val integer DEFAULT 10, offset_val integer DEFAULT 0, sort_by text DEFAULT 'created_at'::text, sort_order text DEFAULT 'desc'::text, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF auto_playbook
 LANGUAGE sql
 STABLE
AS $function$
    SELECT *
    FROM auto_playbook ap
    WHERE
        (
            "hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL
            OR ap."tenant_id" = ("hasura_session" ->> 'x-hasura-user-tenant-id')::uuid
        )
        AND
        (ap.account_id = playbook_account_id :: uuid) AND
        (playbook_status IS NULL OR status = playbook_status) AND
        (playbook_name IS NULL OR name ILIKE '%' || playbook_name || '%') AND
        (
    type_filter IS NULL 
    OR 
    (
        (trigger->'event'->>'type' = type_filter) 
        OR 
        (type_filter = 'schedule' AND trigger->>'schedule' IS NOT NULL)
    )
)
        AND
        (name_filter IS NULL OR EXISTS (
            SELECT 1
            FROM jsonb_array_elements(resource_filter) AS rf
            WHERE rf->>'name' = name_filter
        )) 
        ORDER BY 
        (case when sort_by = 'created_at' and sort_order = 'asc' then ap.created_at end)  asc,
        (case when sort_by = 'created_at' and sort_order = 'desc' then ap.created_at end)  desc,
        (case when sort_by = 'last_executed_time' and sort_order = 'asc' then ap.last_executed_time end)  asc,
        (case when sort_by = 'last_executed_time' and sort_order = 'desc' then ap.last_executed_time end)  desc,
        (case when sort_by = 'name' and sort_order = 'asc' then ap.name end)  asc,
        (case when sort_by = 'name' and sort_order = 'desc' then ap.name end)  desc
        LIMIT limit_val OFFSET offset_val;
$function$;

CREATE OR REPLACE FUNCTION public.spend_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, spend_date_unit text DEFAULT 'date'::text, "limit" integer DEFAULT 100, "offset" integer DEFAULT 0, sort_by text DEFAULT 'spend_amount'::text, sort_order text DEFAULT 'asc'::text, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF spend_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
	( CASE WHEN 'tenant_id' = ANY(group_by) THEN s.tenant ELSE null end ) AS tenant_id,
	( CASE WHEN 'account_id' = ANY(group_by) THEN s.cloud_account ELSE null END) AS account_id,
	( CASE WHEN 'account_name' = ANY(group_by) THEN ca.account_name ELSE null END) AS account_name,
	( CASE WHEN 'account_cloud_provider' = ANY(group_by) THEN ca.cloud_provider ELSE null END) AS account_cloud_provider,
	( CASE WHEN 'resource_id' = ANY(group_by) THEN s.cloud_resource_id ELSE null END) AS resource_id,
	( CASE WHEN 'resource_name' = ANY(group_by) THEN cr."name" ELSE null END) AS resource_name,
	( CASE WHEN 'resource_service_name' = ANY(group_by) THEN cr.service_name ELSE null END) AS resource_service_name,
	( CASE WHEN 'resource_region' = ANY(group_by) THEN cr.region ELSE null END) AS resource_region,
	( CASE WHEN 'resource_type' = ANY(group_by) THEN cr."type" ELSE null END) AS resource_type,
	( CASE WHEN 'spend_date' = ANY(group_by) THEN date_trunc(spend_date_unit, s."date") ELSE null END) AS spend_date,
	count(distinct cr.id) AS resource_count,
	sum(s.amount) AS spend_amount,
    count(DISTINCT cr.service_name) AS service_count,
    count(DISTINCT concat(cr.service_name, cr.region)) AS region_service_count
FROM spends s 
left join cloud_resourses cr on cr.id = s.cloud_resource_id
left join cloud_accounts ca on ca.id = s.cloud_account
WHERE ("hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL OR ( s.tenant = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid))
	AND ("where" #>> '{account_id,_eq}' IS null OR (s.cloud_account = ("where" #>> '{account_id,_eq}') :: uuid))
	AND ("where" #>> '{account_id,_in}' IS null OR ( ("where" #>> '{account_id,_in}')::jsonb ? s.cloud_account::text))
	AND ("where" #>> '{account_name,_eq}' IS null OR ( ca.account_name = ("where" #>> '{account_name,_eq}')))
	AND ("where" #>> '{account_name,_in}' IS null OR ( ("where" #>> '{account_name,_in}')::jsonb ? ca.account_name))
	AND ("where" #>> '{account_cloud_provider,_eq}' IS NULL OR (ca.cloud_provider = ("where" #>> '{account_cloud_provider,_eq}')))
	AND ("where" #>> '{account_cloud_provider,_in}' IS null OR (("where" #>> '{account_cloud_provider,_in}')::jsonb ? ca.cloud_provider))
	AND ("where" #>> '{resource_region,_eq}' IS null OR (cr.region = ("where" #>> '{resource_region,_eq}')))
	AND ("where" #>> '{resource_region,_in}' IS null OR (("where" #>> '{resource_region,_in}')::jsonb ? cr.region))
	AND ("where" #>> '{resource_id,_eq}' IS null OR ( s.cloud_resource_id = ("where" #>> '{resource_id,_eq}') :: uuid ))
	AND ("where" #>> '{resource_id,_in}' IS null OR ( ("where" #>> '{resource_id,_in}')::jsonb ? s.cloud_resource_id::text))
	AND ("where" #>> '{resource_name,_eq}' IS null OR ( cr."name" = ("where" #>> '{resource_name,_eq}')))
	AND ("where" #>> '{resource_name,_in}' IS null OR ( ("where" #>> '{resource_name,_in}')::jsonb ? cr."name"))
	AND ("where" #>> '{resource_service_name,_eq}' IS null OR ( cr.service_name = ("where" #>> '{resource_service_name,_eq}')))
	AND ("where" #>> '{resource_service_name,_in}' IS null OR ( ("where" #>> '{resource_service_name,_in}')::jsonb ? cr.service_name))
	AND ("where" #>> '{resource_service_id,_in}' IS null OR ( ("where" #>> '{resource_service_id,_in}')::jsonb ? concat(s.cloud_account, '.', cr.service_name)))
	AND ("where" #>> '{resource_type,_eq}' IS null OR ( cr.type = ("where" #>> '{resource_type,_eq}')))
	AND ("where" #>> '{resource_type,_ne}' IS null OR ( cr.type != ("where" #>> '{resource_type,_ne}')))
	AND ("where" #>> '{resource_type,_in}' IS null OR ( ("where" #>> '{resource_type,_in}')::jsonb ? cr.type))
	AND ("where" #>> '{spend_date,_eq}' IS NULL OR (s."date" :: date = ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_gt}' IS NULL OR (s."date" :: date > ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_lt}' IS NULL OR (s."date" :: date < ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_le}' IS NULL OR (s."date" :: date <= ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_ge}' IS NULL OR (s."date" :: date >= ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_between,_le}' IS NULL OR (s."date" :: date <= ("where" #>> '{spend_date,_between,_le}')::date))
	AND ("where" #>> '{spend_date,_between,_lt}' IS null OR (s."date" :: date < ("where" #>> '{spend_date,_between,_lt}')::date))
	AND ("where" #>> '{spend_date,_between,_gt}' IS NULL OR (s."date" :: date > ("where" #>> '{spend_date,_between,_gt}')::date))
	AND ("where" #>> '{spend_date,_between,_ge}' IS null OR (s."date" :: date >= ("where" #>> '{spend_date,_between,_ge}')::date))
	AND ("where" #>> '{exclude_aggregate,_eq}' IS null OR (s."exclude_aggregate" :: boolean = ("where" #>> '{exclude_aggregate,_eq}')::boolean))
GROUP BY
	(CASE WHEN 'tenant_id' = ANY(group_by) THEN s.tenant END),
	(CASE WHEN 'account_id' = ANY(group_by) THEN s.cloud_account END),
	(CASE WHEN 'account_name' = ANY(group_by) THEN ca.account_name END),
	(CASE WHEN 'account_cloud_provider' = ANY(group_by) THEN ca.cloud_provider END),
	(CASE WHEN 'resource_id' = ANY(group_by) THEN s.cloud_resource_id END),
	(CASE WHEN 'resource_name' = ANY(group_by) THEN cr."name" END),
	(CASE WHEN 'resource_service_name' = ANY(group_by) THEN cr.service_name END),
	(CASE WHEN 'resource_region' = ANY(group_by) THEN cr.region END),
	(CASE WHEN 'resource_type' = ANY(group_by) THEN cr."type" END),
	(CASE WHEN 'spend_date' = ANY(group_by) THEN date_trunc(spend_date_unit, s."date") END)
ORDER BY 
	(case when sort_by = 'spend_date' and sort_order = 'asc' then max( date_trunc(spend_date_unit, s."date")) end)  asc,
	(case when sort_by = 'spend_date' and sort_order = 'desc' then max( date_trunc(spend_date_unit, s."date")) end)  desc,
	(case when sort_by = 'spend_amount' and sort_order = 'asc' then sum(s.amount) end)  asc,
	(case when sort_by = 'spend_amount' and sort_order = 'desc' then sum(s.amount) end)  desc
LIMIT "limit" OFFSET "offset"
$function$;

CREATE OR REPLACE FUNCTION public.recommendation_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, "limit" integer DEFAULT 100, "offset" integer DEFAULT 0, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF recommendation_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
	( CASE WHEN 'tenant_id' = ANY(group_by) THEN r.tenant_id ELSE null end ) AS tenant_id,
	( CASE WHEN 'account_id' = ANY(group_by) THEN r.cloud_account_id ELSE null END) AS account_id,
	( CASE WHEN 'account_name' = ANY(group_by) THEN ca.account_name ELSE null END) AS account_name,
	( CASE WHEN 'account_cloud_provider' = ANY(group_by) THEN ca.cloud_provider ELSE null END) AS account_cloud_provider,
	( CASE WHEN 'resource_id' = ANY(group_by) THEN r.resource_id ELSE null END) AS resource_id,
	( CASE WHEN 'resource_name' = ANY(group_by) THEN cr."name" ELSE null END) AS resource_name,
	( CASE WHEN 'resource_service_name' = ANY(group_by) THEN cr.service_name ELSE null END) AS resource_service_name,
	( CASE WHEN 'resource_region' = ANY(group_by) THEN cr.region ELSE null END) AS resource_region,
	( CASE WHEN 'spend_date' = ANY(group_by) THEN s."date" ELSE null END) AS spend_date,
	( CASE WHEN 'recommendation_category' = ANY(group_by) THEN r.category ELSE null END) AS recommendation_category,
	( CASE WHEN 'recommendation_rule_name' = ANY(group_by) THEN r.rule_name ELSE null END) AS recommendation_rule_name,
	( CASE WHEN 'recommendation_status' = ANY(group_by) THEN r.status ELSE null END) AS recommendation_status,
	count(distinct cr.id) AS resource_count,
	count(distinct r.id) AS recommendation_count,
	sum(s.amount) AS spend_amount,
	avg(r.estimated_savings) AS estimated_saving
FROM recommendation r
left join cloud_resourses cr on cr.id = r.resource_id 
left join cloud_accounts ca on ca.id = r.cloud_account_id
left join spends s on s.cloud_resource_id = r.resource_id and s.cloud_account = r.cloud_account_id 
WHERE ("hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL OR ( r.tenant_id = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid))
	AND ("where" #>> '{account_id,_eq}' IS null OR (r.cloud_account_id = ("where" #>> '{account_id,_eq}') :: uuid))
	AND ("where" #>> '{account_name,_eq}' IS null OR ( ca.account_name = ("where" #>> '{account_name,_eq}')))
	AND ("where" #>> '{account_cloud_provider,_eq}' IS NULL OR (ca.cloud_provider = ("where" #>> '{account_cloud_provider,_eq}')))
	AND ("where" #>> '{resource_region,_eq}' IS null OR (cr.region = ("where" #>> '{resource_region,_eq}')))
	AND ("where" #>> '{resource_id,_eq}' IS null OR ( r.resource_id = ("where" #>> '{resource_id,_eq}') :: uuid ))
	AND ("where" #>> '{resource_name,_eq}' IS null OR ( cr."name" = ("where" #>> '{resource_name,_eq}')))
	AND ("where" #>> '{resource_service_name,_eq}' IS null OR ( cr.service_name = ("where" #>> '{resource_service_name,_eq}')))
	AND ("where" #>> '{resource_service_name,_ne}' IS null OR ( cr.service_name != ("where" #>> '{resource_service_name,_ne}')))
	AND ("where" #>> '{recommendation_category,_eq}' IS null OR ( r.category = ("where" #>> '{recommendation_category,_eq}')))
	AND ("where" #>> '{recommendation_rule_name,_eq}' IS null OR ( r.rule_name = ("where" #>> '{recommendation_rule_name,_eq}')))
	AND ("where" #>> '{recommendation_status,_eq}' IS null OR ( r.status = ("where" #>> '{recommendation_status,_eq}')))
	AND ("where" #>> '{spend_date,_eq}' IS NULL OR (s."date" :: date = ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_gt}' IS NULL OR (s."date" :: date > ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_lt}' IS NULL OR (s."date" :: date < ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_le}' IS NULL OR (s."date" :: date <= ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_ge}' IS NULL OR (s."date" :: date >= ("where" #>> '{spend_date,_eq}')::date))
	AND ("where" #>> '{spend_date,_between,le}' IS NULL OR (s."date" :: date <= ("where" #>> '{spend_date,_between,le}')::date))
	AND ("where" #>> '{spend_date,_between,lt}' IS null OR (s."date" :: date < ("where" #>> '{spend_date,_between,lt}')::date))
	AND ("where" #>> '{spend_date,_between,gt}' IS NULL OR (s."date" :: date > ("where" #>> '{spend_date,_between,gt}')::date))
	AND ("where" #>> '{spend_date,_between,ge}' IS null OR (s."date" :: date >= ("where" #>> '{spend_date,_between,ge}')::date))
	AND ("where" #>> '{estimated_saving,_gt}' IS null OR (r."estimated_savings" > ("where" #>> '{estimated_saving,_gt}')::float))
	AND ("where" #>> '{estimated_saving,_ge}' IS null OR (r."estimated_savings" >= ("where" #>> '{estimated_saving,_ge}')::float))
	AND ("where" #>> '{estimated_saving,_lt}' IS null OR (r."estimated_savings" < ("where" #>> '{estimated_saving,_lt}')::float))
	AND ("where" #>> '{estimated_saving,_le}' IS null OR (r."estimated_savings" <= ("where" #>> '{estimated_saving,_le}')::float))
GROUP BY
	(CASE WHEN 'tenant_id' = ANY(group_by) THEN r.tenant_id END),
	(CASE WHEN 'account_id' = ANY(group_by) THEN r.cloud_account_id END),
	(CASE WHEN 'account_name' = ANY(group_by) THEN ca.account_name END),
	(CASE WHEN 'account_cloud_provider' = ANY(group_by) THEN ca.cloud_provider END),
	(CASE WHEN 'resource_id' = ANY(group_by) THEN r.resource_id END),
	(CASE WHEN 'resource_name' = ANY(group_by) THEN cr."name" END),
	(CASE WHEN 'resource_service_name' = ANY(group_by) THEN cr.service_name END),
	(CASE WHEN 'resource_region' = ANY(group_by) THEN cr.region END),
	(CASE WHEN 'recommendation_category' = ANY(group_by) THEN r.category END),
	(CASE WHEN 'recommendation_rule_name' = ANY(group_by) THEN r.rule_name END),
	(CASE WHEN 'recommendation_status' = ANY(group_by) THEN r.status END),
	(CASE WHEN 'spend_date' = ANY(group_by) THEN s."date" end)
ORDER BY avg(r.estimated_savings) desc
limit "limit" offset "offset"
$function$;

CREATE OR REPLACE FUNCTION public.event_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, date_unit text DEFAULT 'day'::text, date_unit_bin integer DEFAULT 1, "limit" integer DEFAULT 100, "offset" integer DEFAULT 0, sort_by text DEFAULT 'created_at'::text, sort_order text DEFAULT 'asc'::text, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF event_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN tenant
      ELSE NULL
    END
  ) AS tenant_id,
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN cloud_account_id
      ELSE NULL
    END
  ) AS account_id,
  (
    CASE
      WHEN 'resource_id' = ANY(group_by) THEN cloud_resource_id
      ELSE NULL
    END
  ) AS resource_id,
  (
    CASE
      WHEN 'status' = ANY(group_by) THEN status
      ELSE NULL
    END
  ) AS status,
  (
    CASE
      WHEN 'service_key' = ANY(group_by) THEN service_key
      ELSE NULL
    END
  ) AS service_key,
  (
    CASE
      WHEN 'subject_node' = ANY(group_by) THEN subject_node
      ELSE NULL
    END
  ) AS subject_node,
  (
    CASE
      WHEN 'subject_namespace' = ANY(group_by) THEN subject_namespace
      ELSE NULL
    END
  ) AS subject_namespace,
  (
    CASE
      WHEN 'subject_name' = ANY(group_by) THEN subject_name
      ELSE NULL
    END
  ) AS subject_name,
  (
    CASE
      WHEN 'subject_type' = ANY(group_by) THEN subject_type
      ELSE NULL
    END
  ) AS subject_type,
  (
    CASE
      WHEN 'priority' = ANY(group_by) THEN priority
      ELSE NULL
    END
  ) AS priority,
  (
    CASE
      WHEN 'category' = ANY(group_by) THEN category
      ELSE NULL
    END
  ) AS category,
  (
    CASE
      WHEN 'finding_type' = ANY(group_by) THEN finding_type
      ELSE NULL
    END
  ) AS finding_type,
  (
    CASE
      WHEN 'aggregation_key' = ANY(group_by) THEN aggregation_key
      ELSE NULL
    END
  ) AS aggregation_key,
  (
    CASE
      WHEN 'source' = ANY(group_by) THEN source
      ELSE NULL
    END
  ) AS source,
  (
    CASE
      WHEN 'created_at' = ANY(group_by) THEN date_bin(
        (date_unit_bin || ' ' || date_unit)::interval,
        created_at::timestamp,
        '2001-01-01'::timestamp
      )
      ELSE NULL
    END
  ) AS created_at,
  max(created_at) as max_created_at,
  min(created_at) as min_created_at,
  count(*) AS event_count,
  (
    CASE
      WHEN 'title' = ANY(group_by) THEN title
      ELSE NULL
    END
  ) AS source
FROM events
WHERE (
    "hasura_session"->>'x-hasura-user-tenant-id' IS NULL
    OR (
      "tenant" = ("hasura_session"->>'x-hasura-user-tenant-id')::uuid
    )
  )
  AND (
    "where"#>>'{account_id,_eq}' IS NULL
    OR (
      "cloud_account_id" = ("where"#>>'{account_id,_eq}')::uuid
    )
  )
  AND (
    "where"#>>'{resource_id,_eq}' IS NULL
    OR (
      "cloud_resource_id" = ("where"#>>'{resource_id,_eq}')::uuid
    )
  )
  AND (
    "where"#>>'{status,_eq}' IS NULL
    OR (
      "status" = ("where"#>>'{status,_eq}')
    )
  )
  AND (
    "where"#>>'{service_key,_eq}' IS NULL
    OR (
      "service_key" = ("where"#>>'{service_key,_eq}')
    )
  )
  AND (
    "where"#>>'{subject_node,_eq}' IS NULL
    OR (
      "subject_node" = ("where"#>>'{subject_node,_eq}')
    )
  )
  AND (
    "where"#>>'{subject_namespace,_eq}' IS NULL
    OR (
      "subject_namespace" = ("where"#>>'{subject_namespace,_eq}')
    )
  )
  AND (
    "where"#>>'{subject_name,_eq}' IS NULL
    OR ("subject_name" = ("where"#>>'{subject_name,_eq}'))
  )
  and (
    "where"#>>'{subject_type,_eq}' IS NULL
    OR (
      "subject_type" = ("where"#>>'{subject_type,_eq}')
    )
  )
  AND (
    "where"#>>'{priority,_eq}' IS NULL
    OR (
      "priority" = ("where"#>>'{priority,_eq}')
    )
  )
  AND (
    "where"#>>'{category,_eq}' IS NULL
    OR (
      "category" = ("where"#>>'{category,_eq}')
    )
  )
  AND (
    "where"#>>'{finding_type,_eq}' IS NULL
    OR (
      "finding_type" = ("where"#>>'{finding_type,_eq}')
    )
  )
  AND (
    "where"#>>'{aggregation_key,_eq}' IS NULL
    OR ("aggregation_key" = ("where"#>>'{aggregation_key,_eq}'))
  )
  AND (
    "where"#>>'{source,_eq}' IS NULL
    OR ("source" = ("where"#>>'{source,_eq}'))
  )
  AND (
    "where"#>>'{created_at,_gt}' IS NULL
    OR (
      "created_at" > ("where"#>>'{created_at,_gt}')::timestamp
    )
  )
  AND (
    "where"#>>'{created_at,_lt}' IS NULL
    OR (
      "created_at" < ("where"#>>'{created_at,_lt}')::timestamp
    )
  )
  AND (
    "where"#>>'{created_at,_le}' IS NULL
    OR (
      "created_at" <= ("where"#>>'{created_at,_le}')::timestamp
    )
  )
  AND (
    "where"#>>'{created_at,_ge}' IS NULL
    OR (
      "created_at" >= ("where"#>>'{created_at,_ge}')::timestamp
    )
  )
  AND (
    "where"#>>'{created_at,_between}' IS NULL
    OR (
      (
        "created_at" >= ("where"#>>'{created_at,_between,_ge}')::timestamp
      )
      and (
        "created_at" <= ("where"#>>'{created_at,_between,_le}')::timestamp
      )
    )
  )
GROUP BY (
    CASE
      WHEN 'tenant_id' = ANY(group_by) THEN tenant
    END
  ),
  (
    CASE
      WHEN 'account_id' = ANY(group_by) THEN cloud_account_id
    END
  ),
  (
    CASE
      WHEN 'resource_id' = ANY(group_by) THEN cloud_resource_id
    END
  ),
  (
    CASE
      WHEN 'status' = ANY(group_by) THEN status
    END
  ),
  (
    CASE
      WHEN 'service_key' = ANY(group_by) THEN service_key
    END
  ),
  (
    CASE
      WHEN 'subject_node' = ANY(group_by) THEN subject_node
    END
  ),
  (
    CASE
      WHEN 'subject_namespace' = ANY(group_by) THEN subject_namespace
    END
  ),
  (
    CASE
      WHEN 'subject_name' = ANY(group_by) THEN subject_name
    END
  ),
  (
    CASE
      WHEN 'subject_type' = ANY(group_by) THEN subject_type
    END
  ),
  (
    CASE
      WHEN 'priority' = ANY(group_by) THEN priority
    END
  ),
  (
    CASE
      WHEN 'category' = ANY(group_by) THEN category
    END
  ),
  (
    CASE
      WHEN 'finding_type' = ANY(group_by) THEN finding_type
    END
  ),
  (
    CASE
      WHEN 'aggregation_key' = ANY(group_by) THEN aggregation_key
    END
  ),
  (
    CASE
      WHEN 'source' = ANY(group_by) THEN source
    END
  ),
  (
    CASE
      WHEN 'title' = ANY(group_by) THEN title
    END
  ),
  (
    CASE
      WHEN 'created_at' = ANY(group_by) THEN date_bin(
        (date_unit_bin || ' ' || date_unit)::interval,
        created_at::timestamp,
        '2001-01-01'::timestamp
      )
    END
  )
ORDER BY (
    case
      when sort_by = 'created_at'
      and sort_order = 'asc' then max(
        date_bin(
          (date_unit_bin || ' ' || date_unit)::interval,
          created_at::timestamp,
          '2001-01-01'::timestamp
        )
      )
    end
  ) asc,
  (
    case
      when sort_by = 'created_at'
      and sort_order = 'desc' then max(
        date_bin(
          (date_unit_bin || ' ' || date_unit)::interval,
          created_at::timestamp,
          '2001-01-01'::timestamp
        )
      )
    end
  ) desc
LIMIT "limit" OFFSET "offset";
$function$;

CREATE OR REPLACE FUNCTION public.cloud_resource_metrics_groupings(group_by text[] DEFAULT '{}'::text[], "where" json DEFAULT NULL::json, date_unit text DEFAULT 'day'::text, "limit" integer DEFAULT 100, "offset" integer DEFAULT 0, sort_by text DEFAULT 'timestamp'::text, sort_order text DEFAULT 'asc'::text, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF cloud_resource_metrics_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
SELECT
     (CASE WHEN 'tenant_id' = ANY(group_by) THEN crm.tenant_id ELSE null end ) AS tenant_id,
     (CASE WHEN 'account_name' = ANY(group_by) THEN NULL END) AS account_name, 
     (CASE WHEN 'account_id' = ANY(group_by) THEN crm.cloud_account_id ELSE NULL END) AS account_id, 
     (CASE WHEN 'resource_id' = ANY(group_by) THEN crm.cloud_resource_id ELSE null end ) AS resource_id,
     (CASE WHEN 'resource_name' = ANY(group_by) THEN null end ) AS resource_name,
     (CASE WHEN 'metric' = ANY(group_by) THEN crm.metric ELSE NULL END) AS metric,
     (CASE WHEN 'timestamp' = ANY(group_by) THEN date_trunc(date_unit, crm."timestamp") ELSE NULL END) AS "timestamp",
     COUNT(*) AS count_value,
     SUM(crm.value) AS sum_value,
     AVG(crm.value) AS avg_value,
     MIN(crm.value) AS min_value,
     MAX(crm.value) AS max_value
FROM cloud_resource_metrics crm
WHERE 
    ("hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL OR ( crm.tenant_id = ("hasura_session" ->> 'x-hasura-user-tenant-id') :: uuid))
    AND ("where" #>> '{account_id,_eq}' IS null OR (crm.cloud_account_id = ("where" #>> '{account_id,_eq}') :: uuid))
    AND ("where" #>> '{metric,_eq}' IS null OR (crm.metric = ("where" #>> '{metric,_eq}')))
    AND ("where" #>> '{metric,_in}' IS null OR (("where" #>> '{metric,_in}')::jsonb ? crm.metric::text))
    AND ("where" #>> '{resource_id,_eq}' IS null OR (crm.cloud_resource_id = ("where" #>> '{resource_id,_eq}')::uuid))
    AND ("where" #>> '{timestamp,_lt}' IS NULL OR (crm."timestamp" < ("where" #>> '{timestamp,_lt}')::timestamp))
    AND ("where" #>> '{timestamp,_gt}' IS NULL OR (crm."timestamp" > ("where" #>> '{timestamp,_gt}')::timestamp))
    AND ("where" #>> '{timestamp,_le}' IS NULL OR (crm."timestamp" <= ("where" #>> '{timestamp,_lt}')::timestamp))
    AND ("where" #>> '{timestamp,_ge}' IS NULL OR (crm."timestamp" >= ("where" #>> '{timestamp,_lt}')::timestamp))
    AND ("where" #>> '{timestamp,_between}' IS NULL OR (("timestamp" >= ("where" #>> '{timestamp,_between,_ge}')::timestamp) and ("timestamp" <= ("where" #>> '{timestamp,_between,_le}')::timestamp)))
GROUP BY
    (CASE WHEN 'tenant_id' = ANY(group_by) THEN crm.tenant_id END),
    (CASE WHEN 'account_id' = ANY(group_by) THEN crm.cloud_account_id END),
    (CASE WHEN 'resource_id' = ANY(group_by) THEN crm.cloud_resource_id END),
    (CASE WHEN 'metric' = ANY(group_by) THEN crm.metric END),
    (CASE WHEN 'k8s_workload_name' = ANY(group_by) THEN crm.tags ->> 'controller' END),
    (CASE WHEN 'k8s_pod_name' = ANY(group_by) THEN crm.tags ->> 'name' END),
    (CASE WHEN 'k8s_namespace_name' = ANY(group_by) THEN crm.tags ->> 'namespace' END),
    (CASE WHEN 'k8s_node_name' = ANY(group_by) THEN crm.tags ->> 'node' END),
    (CASE WHEN 'timestamp' = ANY(group_by) THEN date_trunc(date_unit, crm.timestamp) END)
ORDER BY
    (case when sort_by = 'timestamp' and sort_order = 'asc' then max(date_trunc(date_unit, crm.timestamp)) end) asc,
    (case when sort_by = 'timestamp' and sort_order = 'desc' then max(date_trunc(date_unit, crm.timestamp)) end) desc
LIMIT "limit" OFFSET "offset";
$function$;

CREATE OR REPLACE FUNCTION public.auto_playbook_groupings(playbook_account_id text, playbook_status text DEFAULT NULL::text, playbook_name text DEFAULT NULL::text, type_filter text DEFAULT NULL::text, name_filter text DEFAULT NULL::text, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF auto_playbook_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
    SELECT count(*)
    FROM auto_playbook ap
    WHERE
        (
            "hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL
            OR ap."tenant_id" = ("hasura_session" ->> 'x-hasura-user-tenant-id')::uuid
        )
        AND
        (ap.account_id = playbook_account_id :: uuid) AND
        (playbook_status IS NULL OR status = playbook_status) AND
        (playbook_name IS NULL OR name LIKE '%' || playbook_name || '%') AND
        (
    type_filter IS NULL 
    OR 
    (
        (trigger->'event'->>'type' = type_filter) 
        OR 
        (type_filter = 'schedule' AND trigger->>'schedule' IS NOT NULL)
    )
)
        AND
        (name_filter IS NULL OR EXISTS (
            SELECT 1
            FROM jsonb_array_elements(resource_filter) AS rf
            WHERE rf->>'name' = name_filter
        ));
$function$;
