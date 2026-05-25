-- Memory Architecture Phase 1: append-only event log.
-- Source of truth for every memory mutation. Typed stores (soul, preferences,
-- patterns, decisions, collective) are projections rebuildable from this log,
-- so losing a typed row is recoverable — losing an event row is not.

CREATE TABLE IF NOT EXISTS llm_memory_events (
    -- Server-generated UUID so clients can't collide IDs. No PK / unique
    -- constraint on this column — see comment near the end of the table
    -- definition for the rationale.
    id              UUID NOT NULL DEFAULT gen_random_uuid(),

    -- Hard isolation boundary: every index and query filters by tenant_id first.
    tenant_id       VARCHAR(255) NOT NULL,

    -- Nullable: tenant-scoped events (e.g. collective knowledge curated by an
    -- admin) have no user. Per-user events (soul/preference changes) fill it in.
    user_id         VARCHAR(255),

    -- Nullable: events like 'soul.updated' are module-agnostic. Module-scoped
    -- events (e.g. k8s-specific preferences) name the module so projections
    -- can route correctly.
    agent_module    VARCHAR(32),

    -- Short closed vocabulary — 'soul.updated', 'preference.set', 'fact.extracted'.
    -- Projection switch in observe.go fans out per event_type.
    event_type      VARCHAR(64) NOT NULL,

    -- JSONB because every event_type carries a different shape. Validated at
    -- the Go struct layer, not in the column definition — schema-on-read keeps
    -- new event types from needing a migration.
    payload         JSONB NOT NULL DEFAULT '{}',

    -- Who triggered the write. 'user' = explicit admin UI action,
    -- 'agent' = auto-extraction from a conversation turn, 'system' = backfill
    -- or maintenance job, 'admin' = operator tooling. Drives audit filtering.
    actor_kind      VARCHAR(16) NOT NULL,

    -- Nullable identifier for the actor (user_id, agent_name, job_name).
    actor_id        VARCHAR(255),

    -- Nullable dedup key for replayable writers. Backfill sets this to the
    -- legacy row id so a re-run of `memory backfill` skips already-projected
    -- rows. Live extraction leaves it empty (every agent turn is unique).
    idempotency_key VARCHAR(128),

    -- Row creation time. Doubles as the partition key (see PARTITION BY).
    created_at      TIMESTAMP NOT NULL DEFAULT NOW()

    -- No PRIMARY KEY: Postgres requires the partition column in any unique
    -- constraint on a partitioned table, so the only legal PK would be
    -- (id, created_at). But id is never queried alone — every read filters
    -- by tenant_id first via the explicit indexes below. UUID collisions are
    -- vanishingly improbable, and the projections (soul/prefs/patterns/etc.)
    -- are themselves idempotent upserts, so duplicate event rows would only
    -- waste audit-log space, not corrupt state. Keeping no PK avoids the
    -- composite-key footgun without losing anything operationally.
)
-- Partitioned so that (a) old data can be detached/archived without rewriting
-- live rows, and (b) index bloat is bounded per partition. Range by month
-- matches the granularity at which we expect to archive.
PARTITION BY RANGE (created_at);

-- Seed initial monthly partitions. A scheduled maintenance job
-- (memory_partition_maint) creates future partitions ahead of rollover.
-- If it ever fails, inserts to an uncovered month will error loudly, which is
-- preferable to silently writing to a default partition.
CREATE TABLE IF NOT EXISTS llm_memory_events_2026_04 PARTITION OF llm_memory_events
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE IF NOT EXISTS llm_memory_events_2026_05 PARTITION OF llm_memory_events
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE IF NOT EXISTS llm_memory_events_2026_06 PARTITION OF llm_memory_events
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
CREATE TABLE IF NOT EXISTS llm_memory_events_2026_07 PARTITION OF llm_memory_events
    FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');

-- Tenant-wide audit scans: "show me recent events across this tenant".
-- DESC on created_at matches the common pagination order.
CREATE INDEX IF NOT EXISTS idx_mem_events_tenant_time
    ON llm_memory_events (tenant_id, created_at DESC);

-- Per-user replay/audit: "show me everything that happened for this user".
-- Partial WHERE avoids indexing tenant-scoped rows (user_id IS NULL) which
-- would never match the user filter anyway.
CREATE INDEX IF NOT EXISTS idx_mem_events_user_time
    ON llm_memory_events (tenant_id, user_id, created_at DESC)
    WHERE user_id IS NOT NULL;

-- Idempotency: Postgres requires the partition column (created_at) to appear
-- in any unique index on a partitioned table. Including it would defeat
-- "same key = no duplicate across time" semantics, so we use a plain BTREE
-- index for fast lookup and enforce idempotency at the DAO layer
-- (SELECT-before-INSERT). Projections are themselves idempotent (soul / prefs
-- are upserts keyed by user), so rare races only cost a redundant event-log
-- row, not incorrect state.
CREATE INDEX IF NOT EXISTS idx_mem_events_idem
    ON llm_memory_events (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
