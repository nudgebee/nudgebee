-- The role a Knowledge Graph node acts as, set by a user: database, cache,
-- message queue, ... plus an optional engine (postgres, redis, ...).
--
-- Keyed by the node id, which is derived from the node's unique key
-- (core.NodeIDFor), so one row applies to a collected, a VM or a declared node
-- alike and keeps applying across rebuilds, a tombstone and recreate, and a
-- declared node being adopted by a collector. Every write of a node re-applies
-- the row (core.ApplyUserRoles in SaveNodes).
--
-- No foreign key on purpose: the user's intent outlives a tombstoned node, so
-- the role comes back when the node does. A row whose node never returns is
-- inert, because only nodes being written are touched. role is validated in
-- application code (core.IsKnownNodeRole), not by a CHECK, so the vocabulary
-- can grow without a migration.
--
-- Lock window: a new empty table and one index on it; no lock on existing tables.
CREATE TABLE IF NOT EXISTS public.kg_node_roles (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      UUID        NOT NULL,
    kg_node_id     UUID        NOT NULL,
    -- The node's unique key when the role was set. Lets a future node-id
    -- re-key recompute kg_node_id, as V864 did for knowledge_graph_node.
    unique_key     TEXT        NOT NULL,
    role           TEXT        NOT NULL,
    engine         TEXT,
    set_by_user_id UUID,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_kg_node_roles_node
    ON public.kg_node_roles (tenant_id, kg_node_id);

DROP TRIGGER IF EXISTS set_public_kg_node_roles_updated_at ON public.kg_node_roles;
CREATE TRIGGER set_public_kg_node_roles_updated_at
    BEFORE UPDATE ON public.kg_node_roles
    FOR EACH ROW EXECUTE PROCEDURE public.set_current_timestamp_updated_at();
