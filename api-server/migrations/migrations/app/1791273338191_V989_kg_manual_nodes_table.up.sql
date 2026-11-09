-- Migration: Manually declared Knowledge Graph nodes
-- Version: V989
-- Description: Nodes a user declares by hand so a manual dependency can point
-- at a resource no collector has discovered. A node in a connected cloud
-- account is built by that cloud's own row classifier, so it carries the
-- unique key (and node id) the collector writes for the same resource: when
-- the collector later ingests it, the declaration is adopted rather than
-- duplicated. A node outside any connected account is tenant-scoped.
-- New table only: no lock on existing data.

CREATE TABLE IF NOT EXISTS public.kg_manual_nodes (
    id                      BIGSERIAL PRIMARY KEY,
    tenant_id               UUID NOT NULL,

    -- Where it lives. NULL account = not in any connected account.
    cloud_account_id        UUID,
    cloud_provider          TEXT,

    -- Identity inputs (immutable after create: they decide the node id).
    node_type               TEXT NOT NULL,
    specific_type           TEXT NOT NULL,
    name                    TEXT NOT NULL,
    region                  TEXT,
    namespace               TEXT,
    network_node_id         UUID,

    -- Descriptive data, editable.
    description             TEXT,
    properties              JSONB NOT NULL DEFAULT '{}'::jsonb,
    labels                  JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- The node as the builder produced it; every build re-emits it from here.
    kg_node_id              UUID NOT NULL,
    unique_key              TEXT NOT NULL,
    base_properties         JSONB NOT NULL,

    -- active: a declared node; adopted: a collector now writes the same node;
    -- possible_duplicate: a collector node that looks like this resource exists
    -- under a different key (duplicate_node_id).
    status                  TEXT NOT NULL DEFAULT 'active',
    duplicate_node_id       UUID,

    declared_by_user_id     UUID,
    is_active               BOOLEAN NOT NULL DEFAULT TRUE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT kg_manual_nodes_status_check CHECK (
        status IN ('active', 'adopted', 'possible_duplicate')
    )
);

CREATE INDEX IF NOT EXISTS idx_kg_manual_nodes_tenant_active
    ON public.kg_manual_nodes (tenant_id) WHERE is_active = TRUE;

-- One active declaration per graph node.
CREATE UNIQUE INDEX IF NOT EXISTS idx_kg_manual_nodes_node
    ON public.kg_manual_nodes (tenant_id, kg_node_id) WHERE is_active = TRUE;

DROP TRIGGER IF EXISTS set_public_kg_manual_nodes_updated_at ON public.kg_manual_nodes;
CREATE TRIGGER set_public_kg_manual_nodes_updated_at
    BEFORE UPDATE ON public.kg_manual_nodes
    FOR EACH ROW
    EXECUTE PROCEDURE public.set_current_timestamp_updated_at();
