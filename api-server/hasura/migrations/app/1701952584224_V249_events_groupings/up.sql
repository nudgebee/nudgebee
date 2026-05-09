

CREATE
OR REPLACE VIEW "public"."event_groupings_type" AS
SELECT
  events.tenant,
  events.cloud_account_id,
  events.cloud_resource_id,
  events.status,
  events.service_key,
  events.subject_node,
  events.subject_namespace,
  events.subject_name,
  events.subject_type,
  events.priority,
  events.category,
  events.finding_type,
  events.aggregation_key,
  events.source,
  events.created_at,
  max(events.created_at) AS max_created_at,
  min(events.created_at) AS min_created_at,
  count(*) AS event_count
FROM
  events
WHERE
  false
GROUP BY
  events.tenant,
  events.cloud_account_id,
  events.cloud_resource_id,
  events.status,
  events.service_key,
  events.subject_node,
  events.subject_namespace,
  events.subject_name,
  events.subject_type,
  events.priority,
  events.category,
  events.finding_type,
  events.aggregation_key,
  events.source,
  events.created_at
ORDER BY
  events.created_at DESC;

DROP view if exists "public"."event_groupings_type";

CREATE
OR REPLACE VIEW "public"."event_groupings_type" AS
SELECT
  events.tenant,
  events.cloud_account_id as account_id,
  events.cloud_resource_id as resource_id,
  events.status,
  events.service_key,
  events.subject_node,
  events.subject_namespace,
  events.subject_name,
  events.subject_type,
  events.priority,
  events.category,
  events.finding_type,
  events.aggregation_key,
  events.source,
  events.created_at,
  max(events.created_at) AS max_created_at,
  min(events.created_at) AS min_created_at,
  count(*) AS event_count
FROM
  events
WHERE
  false
GROUP BY
  events.tenant,
  events.cloud_account_id,
  events.cloud_resource_id,
  events.status,
  events.service_key,
  events.subject_node,
  events.subject_namespace,
  events.subject_name,
  events.subject_type,
  events.priority,
  events.category,
  events.finding_type,
  events.aggregation_key,
  events.source,
  events.created_at
ORDER BY
  events.created_at DESC;

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
  count(*) AS event_count
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

drop view if exists event_groupings_type cascade;

CREATE
OR REPLACE VIEW "public"."event_groupings_type" AS
SELECT
  events.tenant as tenant_id,
  events.cloud_account_id as account_id,
  events.cloud_resource_id as resource_id,
  events.status,
  events.service_key,
  events.subject_node,
  events.subject_namespace,
  events.subject_name,
  events.subject_type,
  events.priority,
  events.category,
  events.finding_type,
  events.aggregation_key,
  events.source,
  events.created_at,
  max(events.created_at) AS max_created_at,
  min(events.created_at) AS min_created_at,
  count(*) AS event_count
FROM
  events
WHERE
  false
GROUP BY
  events.tenant,
  events.cloud_account_id,
  events.cloud_resource_id,
  events.status,
  events.service_key,
  events.subject_node,
  events.subject_namespace,
  events.subject_name,
  events.subject_type,
  events.priority,
  events.category,
  events.finding_type,
  events.aggregation_key,
  events.source,
  events.created_at
ORDER BY
  events.created_at DESC;

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
  count(*) AS event_count
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
