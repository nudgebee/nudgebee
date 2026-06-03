-- Migration: Manual Dependency Declarations for the Knowledge Graph
-- Version: V743
-- Description: User-declared service-to-service dependencies (including
-- cross-stack k8s <-> AWS). Resolved to KG nodes at upload time, re-checked
-- by the manual flow source on each build cycle, and emitted as CALLS /
-- PUBLISHES_TO / SUBSCRIBES_TO edges with source = 'manual'.

CREATE TABLE IF NOT EXISTS public.kg_manual_dependencies (
    id                          BIGSERIAL PRIMARY KEY,
    tenant_id                   UUID NOT NULL,

    -- Source endpoint (loose match: node_type + name required; rest optional)
    source_node_type            TEXT NOT NULL,
    source_name                 TEXT NOT NULL,
    source_namespace            TEXT,
    source_cluster              TEXT,
    source_arn                  TEXT,
    source_account_id           TEXT,
    source_region               TEXT,

    -- Destination endpoint
    dest_node_type              TEXT NOT NULL,
    dest_name                   TEXT NOT NULL,
    dest_namespace              TEXT,
    dest_cluster                TEXT,
    dest_arn                    TEXT,
    dest_account_id             TEXT,
    dest_region                 TEXT,

    -- Edge metadata
    relationship_type           TEXT NOT NULL DEFAULT 'CALLS'
                                  REFERENCES public.knowledge_graph_relationship_types(name)
                                  ON UPDATE RESTRICT ON DELETE RESTRICT,
    notes                       TEXT,
    declared_by_user_id         UUID,

    -- Resolution result (written at upload; re-checked / re-resolved on demand and each build cycle)
    resolved_source_node_id     UUID,
    resolved_dest_node_id       UUID,
    resolution_status           TEXT NOT NULL DEFAULT 'pending',
    -- Allowed values:
    --   'pending'
    --   'resolved'
    --   'source_unmatched'         | 'dest_unmatched'
    --   'source_ambiguous'         | 'dest_ambiguous'
    --   'source_too_many_matches'  | 'dest_too_many_matches'
    --   'invalid_payload'
    --   'node_inactive'
    resolution_error            TEXT,
    source_match_count          INT,
    dest_match_count            INT,
    source_match_candidates     JSONB,
    dest_match_candidates       JSONB,
    last_resolved_at            TIMESTAMPTZ,

    is_active                   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT kg_manual_deps_resolution_status_check CHECK (
        resolution_status IN (
            'pending',
            'resolved',
            'source_unmatched', 'dest_unmatched',
            'source_ambiguous', 'dest_ambiguous',
            'source_too_many_matches', 'dest_too_many_matches',
            'invalid_payload',
            'node_inactive'
        )
    )
);

CREATE INDEX IF NOT EXISTS idx_kg_manual_deps_tenant_active
    ON public.kg_manual_dependencies (tenant_id) WHERE is_active = TRUE;

CREATE INDEX IF NOT EXISTS idx_kg_manual_deps_tenant_status
    ON public.kg_manual_dependencies (tenant_id, resolution_status) WHERE is_active = TRUE;

-- Dedupe key: one active row per (tenant, source endpoint, dest endpoint, relationship_type).
-- Endpoint identity collapses to ARN when present, else namespace/name@cluster.
--
-- Every nullable component (namespace, cluster) is wrapped in COALESCE(..., '')
-- so the concatenation never evaluates to NULL — without that, a single NULL in
-- the chain makes the whole expression NULL, and Postgres permits multiple NULL
-- rows in a UNIQUE index, silently defeating the dedupe contract.
-- The empty-string substitute collapses NULL and '' to the same key on purpose:
-- a row with cluster='' and a row with cluster=NULL refer to the same logical
-- endpoint (an unscoped service), and we want them treated as duplicates.
CREATE UNIQUE INDEX IF NOT EXISTS idx_kg_manual_deps_dedupe
    ON public.kg_manual_dependencies (
        tenant_id, relationship_type,
        source_node_type,
        COALESCE(source_arn, COALESCE(source_namespace, '') || '/' || source_name || '@' || COALESCE(source_cluster, '')),
        dest_node_type,
        COALESCE(dest_arn,   COALESCE(dest_namespace,   '') || '/' || dest_name   || '@' || COALESCE(dest_cluster,   ''))
    ) WHERE is_active = TRUE;

CREATE TRIGGER set_public_kg_manual_dependencies_updated_at
    BEFORE UPDATE ON public.kg_manual_dependencies
    FOR EACH ROW
    EXECUTE PROCEDURE public.set_current_timestamp_updated_at();
