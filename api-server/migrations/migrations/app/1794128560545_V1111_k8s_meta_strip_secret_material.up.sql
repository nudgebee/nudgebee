-- Remove secret material that k8s-collector discovery stored in meta.config of
-- k8s_workloads, k8s_pods and cloud_resourses.
--
-- Discovery stored the agent's config verbatim. Two parts of it carry secrets:
-- the kubectl.kubernetes.io/last-applied-configuration annotation (a full copy
-- of the object, inline env values included; objectset.rio.cattle.io/applied is
-- Rancher/k3s's gzipped equivalent), and containers[].env[].value, which older
-- agents sent. Discovery now cleans config before storing it
-- (collector-server/k8s-collector/app/utils/k8s_config_redaction.py). This
-- applies the same rule to rows already stored. Keep the two in step:
--   * annotations: drop the two keys above, keep every other annotation;
--   * containers[].env[]: a non-empty string value other than '[redacted]'
--     becomes '[redacted]'; name and value_from stay (value_from names the
--     Secret or ConfigMap key, which the knowledge graph links on).
-- Active rows would heal on their next upsert, which replaces meta whole.
-- Inactive rows are never upserted again, so only this cleans them.
--
-- Lock window: each UPDATE scans its table once (nothing indexes meta for
-- these predicates) and takes row locks only on the rows it changes, holding
-- them until commit. Reads are never blocked. A collector upsert of one of
-- those rows waits until this commits, and this waits behind a long collector
-- transaction holding one of them. A collector
-- batch that locks the same rows in another order can deadlock with it;
-- Postgres then aborts one side. If that is this migration, re-run the Job; if
-- it is the collector batch, the next discovery rewrites those rows (cleaned).
-- Re-running is safe: a second run changes nothing. Changed rows get a new
-- updated_at from the table triggers; nothing keys on it for these tables.

DO $$
DECLARE
    spec text[];
    changed bigint;
BEGIN
    -- [table, extra predicate]. cloud_resourses holds every cloud's resources;
    -- only the Kubernetes rows come from discovery.
    FOREACH spec SLICE 1 IN ARRAY ARRAY[
        ['k8s_workloads', 'TRUE'],
        ['k8s_pods', 'TRUE'],
        ['cloud_resourses', 'cloud_provider = ''K8s''']
    ] LOOP
        EXECUTE format($sql$
            UPDATE %1$I AS t
            SET meta = jsonb_set(t.meta, '{config}', (
                SELECT CASE
                           WHEN jsonb_typeof(a.config -> 'containers') = 'array'
                           THEN jsonb_set(a.config, '{containers}', (
                               SELECT COALESCE(jsonb_agg(
                                          CASE
                                              WHEN jsonb_typeof(c.elem -> 'env') = 'array'
                                              THEN jsonb_set(c.elem, '{env}', (
                                                  SELECT COALESCE(jsonb_agg(
                                                             CASE
                                                                 WHEN jsonb_typeof(e.elem -> 'value') = 'string'
                                                                  AND e.elem ->> 'value' NOT IN ('', '[redacted]')
                                                                 THEN jsonb_set(e.elem, '{value}', to_jsonb('[redacted]'::text))
                                                                 ELSE e.elem
                                                             END ORDER BY e.ord), '[]'::jsonb)
                                                  FROM jsonb_array_elements(
                                                           CASE WHEN jsonb_typeof(c.elem -> 'env') = 'array'
                                                                THEN c.elem -> 'env'
                                                                ELSE '[]'::jsonb END)
                                                       WITH ORDINALITY AS e(elem, ord)))
                                              ELSE c.elem
                                          END ORDER BY c.ord), '[]'::jsonb)
                               FROM jsonb_array_elements(
                                        CASE WHEN jsonb_typeof(a.config -> 'containers') = 'array'
                                             THEN a.config -> 'containers'
                                             ELSE '[]'::jsonb END)
                                    WITH ORDINALITY AS c(elem, ord)))
                           ELSE a.config
                       END
                FROM (SELECT CASE
                                 WHEN jsonb_typeof(t.meta #> '{config,annotations}') = 'object'
                                 THEN jsonb_set(t.meta -> 'config', '{annotations}',
                                                (t.meta #> '{config,annotations}')
                                                - 'kubectl.kubernetes.io/last-applied-configuration'
                                                - 'objectset.rio.cattle.io/applied')
                                 ELSE t.meta -> 'config'
                             END AS config) AS a))
            WHERE %2$s
              AND jsonb_typeof(t.meta -> 'config') = 'object'
              AND (
                    (jsonb_typeof(t.meta #> '{config,annotations}') = 'object'
                     AND t.meta #> '{config,annotations}' ?| ARRAY[
                           'kubectl.kubernetes.io/last-applied-configuration',
                           'objectset.rio.cattle.io/applied'])
                 OR EXISTS (
                        SELECT 1
                        -- CASE, not an AND guard: Postgres does not promise to
                        -- evaluate a type check before the set-returning call.
                        FROM jsonb_array_elements(
                                 CASE WHEN jsonb_typeof(t.meta #> '{config,containers}') = 'array'
                                      THEN t.meta #> '{config,containers}'
                                      ELSE '[]'::jsonb END) AS c(elem)
                        CROSS JOIN LATERAL jsonb_array_elements(
                                 CASE WHEN jsonb_typeof(c.elem -> 'env') = 'array'
                                      THEN c.elem -> 'env'
                                      ELSE '[]'::jsonb END) AS e(elem)
                        WHERE jsonb_typeof(e.elem -> 'value') = 'string'
                          AND e.elem ->> 'value' NOT IN ('', '[redacted]'))
              )
        $sql$, spec[1], spec[2]);
        GET DIAGNOSTICS changed = ROW_COUNT;
        RAISE NOTICE 'V1111: cleaned meta.config on % rows of %', changed, spec[1];
    END LOOP;
END
$$;
