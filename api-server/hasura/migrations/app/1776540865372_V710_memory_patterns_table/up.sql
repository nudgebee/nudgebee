-- Memory Architecture Phase 2: inferred behavioral patterns (L2).
-- Captures "this user frequently touches X" facts derived from conversation
-- history (e.g. "frequently investigates service=payments"). Used to bias
-- tool selection and question completion. Auto-extracted only — users never
-- write patterns directly. Semantic retrieval via RAG lands in Phase 2f.

CREATE TABLE IF NOT EXISTS llm_memory_patterns (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    tenant_id     VARCHAR(255) NOT NULL,
    user_id       VARCHAR(255) NOT NULL,

    -- NULL = pattern generalises across modules. Common case is module-scoped
    -- ("in the k8s agent this user frequently investigates nudgebee namespace").
    agent_module  VARCHAR(32),

    -- Closed vocabulary drawn from classifier.go: frequent_resource_type,
    -- preferred_diagnostic_flow, etc. VARCHAR(64) matches event_type width
    -- for consistency.
    pattern_kind  VARCHAR(64) NOT NULL,

    -- The named entity the pattern is about ("payments", "us-east-1",
    -- "kubectl top pods"). 512 is deliberately generous — some subjects are
    -- short commands or resource paths that can exceed VARCHAR(255).
    subject       VARCHAR(512) NOT NULL,

    -- Extra context the renderer might use (e.g. which tool invoked it,
    -- sample query shapes). Shapes vary per pattern_kind; JSONB keeps the
    -- schema open without a per-kind table.
    metadata      JSONB NOT NULL DEFAULT '{}',

    -- Raw observation count. Incremented on every re-extraction. Kept as
    -- an integer (not derived from `score`) so we always know how many
    -- signals we've seen — that's separate from how much we should weight
    -- them today.
    count         INTEGER NOT NULL DEFAULT 1,

    -- Decayed relevance score. Computed from count with recency decay so
    -- a pattern seen 100 times a year ago ranks below one seen 10 times
    -- this week. NUMERIC(10,3) gives 7 digits before the decimal — more
    -- than enough headroom for accumulated scores in realistic usage.
    score         NUMERIC(10,3) NOT NULL DEFAULT 1.0,

    -- Separate from updated_at: updated_at moves on any write (including
    -- score decay recomputes), last_seen_at only moves when the pattern
    -- was actually re-observed in a turn. Drives the recency component of
    -- the score and the "last active" sort in the patterns index.
    last_seen_at  TIMESTAMP NOT NULL DEFAULT NOW(),

    created_at    TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMP NOT NULL DEFAULT NOW()
);

-- One row per (user, module, kind, subject). Second re-extraction updates
-- the existing row (count++, last_seen_at=now, recompute score) instead of
-- inserting a duplicate. COALESCE(module, '') for same NULL-collapsing
-- reason as preferences.
CREATE UNIQUE INDEX IF NOT EXISTS uq_patterns_tenant_user_mod_kind_subject
    ON llm_memory_patterns (tenant_id, user_id, COALESCE(agent_module, ''), pattern_kind, subject);

-- Management UI lookup: list all patterns for a user, optionally filtered by
-- module. Mirrors the preferences lookup index shape.
CREATE INDEX IF NOT EXISTS idx_patterns_lookup
    ON llm_memory_patterns (tenant_id, user_id, agent_module);

-- Compose path: "give me this user's top-N patterns by relevance". score DESC
-- is the primary sort; last_seen_at DESC is the tiebreaker so ties favour
-- more recent observations.
CREATE INDEX IF NOT EXISTS idx_patterns_score
    ON llm_memory_patterns (tenant_id, user_id, score DESC, last_seen_at DESC);
