-- One active manual dependency per endpoint pair, now told apart by account.
--
-- The V743 key was type + COALESCE(arn, namespace/name@cluster) for each side,
-- so two dependencies on same-named nodes in different accounts (an `orders`
-- queue in two AWS accounts, a `checkout` workload in two clusters) collided
-- and the second was refused as already declared, although it points at a
-- different node. A JSON import stores a side that names a node of the same
-- document with that node's account, so the key now includes each side's
-- account qualifier.
--
-- Rebuilding also restores the COALESCE on namespace that one environment's
-- copy of the index had lost, which let a dependency on a resource with no
-- namespace be declared twice.
--
-- The new key only adds columns, so rows that are distinct under the current
-- key stay distinct: the build cannot fail on existing data. The table is
-- small (one row per declared dependency); the DROP + CREATE holds its lock
-- for well under a second.
DROP INDEX IF EXISTS public.idx_kg_manual_deps_dedupe;

CREATE UNIQUE INDEX IF NOT EXISTS idx_kg_manual_deps_dedupe
    ON public.kg_manual_dependencies (
        tenant_id, relationship_type,
        source_node_type,
        COALESCE(source_arn, COALESCE(source_namespace, '') || '/' || source_name || '@' || COALESCE(source_cluster, '')),
        COALESCE(source_account_id, ''),
        dest_node_type,
        COALESCE(dest_arn,   COALESCE(dest_namespace,   '') || '/' || dest_name   || '@' || COALESCE(dest_cluster,   '')),
        COALESCE(dest_account_id, '')
    ) WHERE is_active = TRUE;
