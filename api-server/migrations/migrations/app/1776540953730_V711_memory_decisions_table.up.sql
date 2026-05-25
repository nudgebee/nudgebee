-- Memory Architecture Phase 2: immutable episodic decision log (L2).
-- Every row captures a discrete choice the agent (or user via the agent)
-- made: "picked runbook X", "accepted recommendation Y", "dismissed alert Z".
-- Append-only — corrections are NEW rows referencing the prior decision via
-- `context`, not updates. This is what lets us replay "why did we do X?" and
-- also lets the compose path surface recent decisions as prompt context.

CREATE TABLE IF NOT EXISTS llm_memory_decisions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    tenant_id       VARCHAR(255) NOT NULL,
    user_id         VARCHAR(255) NOT NULL,

    -- Nullable so tenant-scoped decisions (e.g. an admin approving a policy
    -- that affects every conversation) don't need a fake conversation id.
    conversation_id UUID,

    agent_module    VARCHAR(32),

    -- Narrow closed vocabulary (see decisions.go TypeRunbookChosen, etc.) so
    -- the ranker can weight consistently. VARCHAR(64) matches the event
    -- log's event_type width.
    decision_type   VARCHAR(64) NOT NULL,

    -- Human-readable summary — "chose the payments-service restart runbook".
    -- TEXT (not VARCHAR) because subjects can paste user question fragments
    -- of arbitrary length, and we FTS on it anyway.
    subject         TEXT NOT NULL,

    -- Arbitrary structured context at decision time: prior decision id,
    -- candidates considered, tools invoked, etc. Flexible because different
    -- decision_types capture different things.
    context         JSONB NOT NULL DEFAULT '{}',

    -- Optional: filled in later when the outcome is observed (runbook
    -- succeeded, remediation rolled back, etc.). NULL = outcome unknown.
    outcome         JSONB,

    -- Business time: when the agent actually made the call, as agreed with
    -- the user. NOT NULL because every decision has a definite "when" —
    -- but it can differ from created_at (backfill imports a past decision,
    -- or an async worker records a past event).
    decided_at      TIMESTAMP NOT NULL,

    -- Row creation time — when we logged the decision. Separate from
    -- decided_at so we preserve the "as agreed" business timestamp while
    -- still having an audit trail of when the row hit the DB.
    created_at      TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Compose-path lookup: "show me this user's most recent decisions". DESC on
-- decided_at (not created_at) because the renderer surfaces "what you most
-- recently chose", not "what was most recently logged".
CREATE INDEX IF NOT EXISTS idx_decisions_recent
    ON llm_memory_decisions (tenant_id, user_id, decided_at DESC);

-- Keyword search against subject for the RecentForUser(keyword=...) path.
-- GIN on tsvector is the standard Postgres FTS pattern — cheaper than LIKE
-- for the "find decisions about payments" case.
CREATE INDEX IF NOT EXISTS idx_decisions_subject_fts
    ON llm_memory_decisions USING GIN (to_tsvector('english', subject));
