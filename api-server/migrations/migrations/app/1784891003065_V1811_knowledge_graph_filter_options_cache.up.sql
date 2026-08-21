-- Migration: Filter-options read cache for the Knowledge Graph
-- Version: V810
-- Description: Per-tenant cache of the unfiltered kg_get_filter_options payload
-- (account_ids, node_types, label_keys, attribute_keys, node_id_map, node_count,
-- last_sync_time), stored as one JSONB row per tenant. Refreshed at the end of each
-- successful KG build, so it is never staler than the graph itself. The dropdown's
-- initial (unfiltered) load reads this single row instead of running ~6 live
-- DISTINCT scans plus the all-node unique_key -> id query; filtered calls (e.g. the
-- node picker, which passes account/type filters) still query live.

CREATE TABLE IF NOT EXISTS public.knowledge_graph_filter_options (
    tenant_id   UUID PRIMARY KEY,
    payload     JSONB NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
