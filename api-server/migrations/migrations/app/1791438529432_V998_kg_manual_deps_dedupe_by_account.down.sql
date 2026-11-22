-- Restore the V743 key (without the account qualifiers). Fails if rows that
-- differ only by account now exist; deactivate one of each pair first.
DROP INDEX IF EXISTS public.idx_kg_manual_deps_dedupe;

CREATE UNIQUE INDEX IF NOT EXISTS idx_kg_manual_deps_dedupe
    ON public.kg_manual_dependencies (
        tenant_id, relationship_type,
        source_node_type,
        COALESCE(source_arn, COALESCE(source_namespace, '') || '/' || source_name || '@' || COALESCE(source_cluster, '')),
        dest_node_type,
        COALESCE(dest_arn,   COALESCE(dest_namespace,   '') || '/' || dest_name   || '@' || COALESCE(dest_cluster,   ''))
    ) WHERE is_active = TRUE;
