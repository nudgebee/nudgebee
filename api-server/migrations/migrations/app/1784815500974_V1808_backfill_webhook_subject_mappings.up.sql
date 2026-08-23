-- One-time data backfill: copies legacy title->service JSON blobs out of
-- tenant_attrs (keyed by DATADOG_INCIDENT_TITLE_SERVICE_MAPPING /
-- PAGERDUTY_INCIDENT_TITLE_SERVICE_MAPPING / ZENDUTY_INCIDENT_TITLE_SERVICE_MAPPING)
-- into the dedicated webhook_subject_mappings table (V807). tenant_attrs
-- rows are read-only here and left in place as the backup/rollback path.
--
-- Mirrors BackfillWebhookSubjectMappings / upsertBackfillBatch in
-- api-server/services/integrations/webhook_subject_mappings_backfill.go —
-- see that file (and its Go tests) for the reference implementation this
-- was translated from.
--
-- Idempotent: an existing row's services list is merged (new service
-- appended, capped at 5, oldest evicted), never overwritten, so this is
-- safe even if it somehow ran twice. It will not, however, pick up
-- tenant_attrs entries that arrive after this migration has run — unlike
-- the Go backfill's HTTP endpoint (POST /v1/integration/webhook_subject_mappings/backfill),
-- which stays available for that.
--
-- Known gap vs. the Go path: Datadog's k8s-facet normalization
-- (normalizeDatadogServicesFieldValue / parseDatadogK8sSubject) is
-- reimplemented below as a best-effort SQL equivalent of the same
-- precedence chain (kube_deployment > kube_stateful_set > kube_daemon_set >
-- kube_replica_set > kube_replication_controller > kube_job > kube_cronjob >
-- kube_ownerref_name > pod_name/pod > kube_node/node/host). It has no test
-- coverage the way the Go version does. A facet string that doesn't match
-- any recognized key falls back to the raw value unchanged, same as Go.

WITH legacy AS (
    SELECT
        ta.tenant_id,
        ta.name AS attr_key,
        entry   AS raw_entry,
        ord
    FROM tenant_attrs ta,
         jsonb_array_elements(ta.value::jsonb) WITH ORDINALITY AS t(entry, ord)
    WHERE ta.name IN (
        'DATADOG_INCIDENT_TITLE_SERVICE_MAPPING',
        'PAGERDUTY_INCIDENT_TITLE_SERVICE_MAPPING',
        'ZENDUTY_INCIDENT_TITLE_SERVICE_MAPPING'
    )
    AND ta.value IS NOT NULL AND ta.value <> ''
),
cleaned AS (
    SELECT
        tenant_id,
        attr_key,
        trim(raw_entry ->> 'title')   AS title,
        trim(raw_entry ->> 'service') AS raw_service,
        ord
    FROM legacy
    WHERE trim(raw_entry ->> 'title') <> ''
      AND raw_entry ->> 'service' IS NOT NULL
      AND trim(raw_entry ->> 'service') <> ''
),
-- Datadog-only: explode each raw_service into "key:value" tokens (facets can
-- be space- or comma-separated within one entry, same as parseDatadogK8sSubject).
facet_tokens AS (
    SELECT
        tenant_id, attr_key, title, raw_service, ord, tok_pos,
        lower(trim(split_part(tok, ':', 1)))                                        AS k,
        trim(both '"''' from trim(substring(tok from position(':' in tok) + 1)))     AS v
    FROM cleaned,
         unnest(regexp_split_to_array(raw_service, '[\s,]+')) WITH ORDINALITY AS u(tok, tok_pos)
    WHERE attr_key = 'DATADOG_INCIDENT_TITLE_SERVICE_MAPPING'
      AND raw_service LIKE '%:%'
      AND position(':' in tok) > 0
),
facet_first AS (
    -- first occurrence wins per key, mirroring parseDatadogK8sSubject's
    -- `if !seen` — ORDER BY tok_pos breaks ties when the same key repeats
    -- within one facet string (e.g. "pod_name:a,pod_name:b").
    SELECT DISTINCT ON (tenant_id, attr_key, title, raw_service, ord, k)
        tenant_id, attr_key, title, raw_service, ord, k, v
    FROM facet_tokens
    WHERE k <> '' AND v <> '' AND v <> '*'
    ORDER BY tenant_id, attr_key, title, raw_service, ord, k, tok_pos
),
facet_pivot AS (
    SELECT
        tenant_id, attr_key, title, raw_service, ord,
        max(v) FILTER (WHERE k = 'kube_deployment')             AS kube_deployment,
        max(v) FILTER (WHERE k = 'kube_stateful_set')           AS kube_stateful_set,
        max(v) FILTER (WHERE k = 'kube_daemon_set')             AS kube_daemon_set,
        max(v) FILTER (WHERE k = 'kube_replica_set')            AS kube_replica_set,
        max(v) FILTER (WHERE k = 'kube_replication_controller') AS kube_replication_controller,
        max(v) FILTER (WHERE k = 'kube_job')                    AS kube_job,
        max(v) FILTER (WHERE k = 'kube_cronjob')                AS kube_cronjob,
        max(v) FILTER (WHERE k = 'kube_ownerref_name')          AS kube_ownerref_name,
        max(v) FILTER (WHERE k = 'pod_name')                    AS pod_name,
        max(v) FILTER (WHERE k = 'pod')                         AS pod,
        max(v) FILTER (WHERE k = 'kube_node')                   AS kube_node,
        max(v) FILTER (WHERE k = 'node')                        AS node,
        max(v) FILTER (WHERE k = 'host')                        AS host
    FROM facet_first
    GROUP BY tenant_id, attr_key, title, raw_service, ord
),
normalized AS (
    -- Non-Datadog rows, or Datadog values with no ':' (already a clean
    -- name), pass through unchanged; a Datadog facet string resolves to the
    -- clean workload/pod/node name, else falls back to the raw value.
    SELECT
        c.tenant_id, c.attr_key, c.title, c.ord,
        CASE
            WHEN c.attr_key <> 'DATADOG_INCIDENT_TITLE_SERVICE_MAPPING' OR c.raw_service NOT LIKE '%:%'
                THEN c.raw_service
            ELSE COALESCE(
                fp.kube_deployment, fp.kube_stateful_set, fp.kube_daemon_set,
                fp.kube_replica_set, fp.kube_replication_controller,
                fp.kube_job, fp.kube_cronjob, fp.kube_ownerref_name,
                fp.pod_name, fp.pod, fp.kube_node, fp.node, fp.host,
                c.raw_service
            )
        END AS service
    FROM cleaned c
    LEFT JOIN facet_pivot fp
        ON fp.tenant_id = c.tenant_id AND fp.attr_key = c.attr_key
       AND fp.title = c.title AND fp.raw_service = c.raw_service AND fp.ord = c.ord
),
-- No dedupe/cap-at-5 needed here: tenant_attrs has a unique (tenant_id, name)
-- constraint and the old writer deduped on title before ever writing
-- (legacyHistoricalIncident: "a title never repeats within one blob"), so
-- there is at most one service per exact title in this data. A case-
-- insensitive collision (two different-cased spellings of the same title
-- in one blob) is the only way this GROUP BY sees more than one row per
-- key, and DISTINCT below just guards against that producing an exact
-- duplicate. The real cap-at-5-oldest-evicted logic still applies below,
-- against whatever's already stored in webhook_subject_mappings — that
-- column can genuinely hold 5 already, accumulated over time from live
-- webhook traffic.
grouped AS (
    SELECT
        tenant_id,
        attr_key,
        (array_agg(title ORDER BY ord))[1] AS title,
        string_agg(DISTINCT service, ',')  AS services
    FROM normalized
    WHERE service <> ''
    GROUP BY tenant_id, attr_key, lower(title)
)
INSERT INTO webhook_subject_mappings (tenant_id, attr_key, title, services)
SELECT tenant_id, attr_key, title, services FROM grouped
ON CONFLICT (tenant_id, attr_key, lower(title)) DO UPDATE
SET services = (
        WITH combined AS (
            SELECT svc, 0 AS seq, pos
            FROM unnest(string_to_array(webhook_subject_mappings.services, ',')) WITH ORDINALITY AS u(svc, pos)
            UNION ALL
            SELECT svc, 1 AS seq, pos
            FROM unnest(string_to_array(excluded.services, ',')) WITH ORDINALITY AS u(svc, pos)
        ),
        positioned AS (
            SELECT svc, row_number() OVER (ORDER BY seq, pos) AS rn
            FROM combined
        ),
        dedup AS (
            SELECT svc, min(rn) AS first_rn
            FROM positioned
            GROUP BY svc
        ),
        final_capped AS (
            SELECT svc, first_rn FROM dedup ORDER BY first_rn DESC LIMIT 5
        )
        SELECT string_agg(svc, ',' ORDER BY first_rn)
        FROM final_capped
    ),
    updated_at = now();
