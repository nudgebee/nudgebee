-- Remove secret material that knowledge-graph nodes copied from Kubernetes
-- annotations.
--
-- kubectl apply stores the whole object in
-- kubectl.kubernetes.io/last-applied-configuration: a Secret's base64 data, a
-- workload's inline env values. The KG stored annotations verbatim, and
-- kg_get_node returned them. The builder now sanitises annotations in
-- core.NewNode (core/annotations.go); this mirrors that rule for rows already
-- stored. Active rows would heal on their next build, inactive (tombstoned)
-- rows never would. Keep the key lists in step with core/annotations.go.
--
-- Lock window: both UPDATEs scan knowledge_graph_node once (no index on
-- properties->'annotations'; dev: ~42k rows, 2.5 GB with TOAST) and take row
-- locks only on the rows they change (dev: 663 + 104, 760 distinct rows).
-- Reads are never
-- blocked; a KG build upserting one of those rows waits until this commits,
-- expected a few seconds. The updated_at trigger moves "Last Seen" on the
-- changed rows; nothing reads annotations from query_attributes /
-- ontology_attributes (checked on dev: 0 rows carry them).

-- 1. Annotations that are a copy of the whole object: every node kind.
UPDATE knowledge_graph_node
SET properties = CASE
        WHEN (properties -> 'annotations')
             - 'kubectl.kubernetes.io/last-applied-configuration'
             - 'objectset.rio.cattle.io/applied' = '{}'::jsonb
        THEN properties - 'annotations'
        ELSE jsonb_set(properties, '{annotations}',
                       (properties -> 'annotations')
                       - 'kubectl.kubernetes.io/last-applied-configuration'
                       - 'objectset.rio.cattle.io/applied')
    END
WHERE jsonb_typeof(properties -> 'annotations') = 'object'
  AND properties -> 'annotations' ?| ARRAY[
        'kubectl.kubernetes.io/last-applied-configuration',
        'objectset.rio.cattle.io/applied'];

-- 2. Secrets: keep the keys, redact every value outside the allowlist.
UPDATE knowledge_graph_node n
SET properties = jsonb_set(n.properties, '{annotations}', (
        SELECT jsonb_object_agg(
                   a.key,
                   CASE WHEN a.key = ANY (ARRAY[
                            'meta.helm.sh/release-name',
                            'meta.helm.sh/release-namespace',
                            'kubernetes.io/service-account.name',
                            'kubernetes.io/service-account.uid',
                            'cert-manager.io/certificate-name',
                            'cert-manager.io/issuer-name',
                            'cert-manager.io/issuer-kind',
                            'cert-manager.io/issuer-group'])
                        THEN a.value
                        ELSE to_jsonb('[redacted]'::text)
                   END)
        FROM jsonb_each(n.properties -> 'annotations') a))
WHERE n.node_type = 'K8sSecret'
  AND jsonb_typeof(n.properties -> 'annotations') = 'object'
  AND EXISTS (
        SELECT 1
        FROM jsonb_each(n.properties -> 'annotations') a
        WHERE a.value <> to_jsonb('[redacted]'::text)
          AND NOT (a.key = ANY (ARRAY[
                'meta.helm.sh/release-name',
                'meta.helm.sh/release-namespace',
                'kubernetes.io/service-account.name',
                'kubernetes.io/service-account.uid',
                'cert-manager.io/certificate-name',
                'cert-manager.io/issuer-name',
                'cert-manager.io/issuer-kind',
                'cert-manager.io/issuer-group'])));
