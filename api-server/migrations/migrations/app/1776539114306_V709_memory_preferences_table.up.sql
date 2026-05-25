-- Memory Architecture Phase 1: typed user preferences.
-- Many rows per user, one row per (module, key). A preference is a small
-- named setting the agent should respect — region, severity filter, output
-- verbosity, default cluster, etc. Unlike the soul (one blob per user),
-- preferences are granular so we can render only the relevant ones into
-- each prompt instead of sending the whole profile every turn.

CREATE TABLE IF NOT EXISTS llm_memory_preferences (
    -- Surrogate PK so the management UI can target a single preference row
    -- without having to send the full composite key in every DELETE/PATCH.
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    tenant_id    VARCHAR(255) NOT NULL,
    user_id      VARCHAR(255) NOT NULL,

    -- NULL = cross-agent (applies to every module). Non-NULL scopes the
    -- preference to one module so e.g. a k8s-specific "default namespace"
    -- doesn't leak into AWS agent prompts.
    agent_module VARCHAR(32),

    -- Free-form identifier within the (user, module) namespace. Capped at
    -- 128 chars because it's printed into prompts and we want to bound size.
    key          VARCHAR(128) NOT NULL,

    -- JSONB so the value can be a string, number, bool, object, or array
    -- without one column per shape.
    value        JSONB NOT NULL,

    -- 'explicit' = user/admin set it via UI, 'inferred' = auto-extracted
    -- from conversation. Renderer prefers explicit over inferred when both
    -- exist at the same key (rare but possible during transition).
    source       VARCHAR(16) NOT NULL DEFAULT 'explicit',

    -- 0.00–1.00 score. Explicit writes default to 1.00; extracted prefs
    -- start at 0.6 in the code (clampConfidence fallback). Low-confidence
    -- prefs are filtered out by the compose path under tight token budgets.
    confidence   NUMERIC(3,2) NOT NULL DEFAULT 1.00,

    created_at   TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP NOT NULL DEFAULT NOW()
);

-- "One row per (user, module, key)" is the real invariant. We express it
-- with COALESCE so NULL agent_module collapses to '' — otherwise two NULLs
-- would be considered distinct by PG's default NULL semantics, letting two
-- cross-agent prefs with the same key coexist. UNIQUE CONSTRAINT doesn't
-- allow expressions, hence the functional unique index.
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_pref_tenant_user_module_key
    ON llm_memory_preferences (tenant_id, user_id, COALESCE(agent_module, ''), key);

-- Hot query: "load all prefs for this user, optionally filtered by module".
-- Covers both the compose path (list all for renderer) and the management
-- UI (list per module).
CREATE INDEX IF NOT EXISTS idx_user_pref_lookup
    ON llm_memory_preferences (tenant_id, user_id, agent_module);
