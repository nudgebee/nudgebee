-- Memory Architecture Phase 1: user soul (stylistic profile).
-- Exactly one row per (tenant, user). Agents never write here — the soul is
-- curated by the user (or an admin on their behalf) via the management UI.
-- Keeping this write-restricted is why we trust it as high-priority context
-- that always makes it into the prompt even when token budgets are tight.

CREATE TABLE IF NOT EXISTS llm_memory_soul (
    -- Isolation boundary. Two tenants can legitimately have a user with the
    -- same external id (SSO user across different accounts), so tenant_id
    -- must be part of the key.
    tenant_id  VARCHAR(255) NOT NULL,

    -- External user id (same shape as elsewhere in the app — not a UUID
    -- because many tenants integrate with upstream IAM systems that emit
    -- opaque string ids).
    user_id    VARCHAR(255) NOT NULL,

    -- Optimistic-concurrency marker. Bumped on every write so a UI that
    -- edited v3 can detect an intervening v4 before clobbering. Default 1
    -- covers first-write case.
    version    INTEGER NOT NULL DEFAULT 1,

    -- Structured fields: tone, verbosity, risk_posture, etc. JSONB (not
    -- individual columns) because the set of style fields evolves faster
    -- than we want to ship schema migrations, and every field is optional.
    style      JSONB NOT NULL DEFAULT '{}',

    -- Freeform user prose — the Claude-Code-style "this is how I work"
    -- paragraph. Kept separate from `style` because it's unstructured and
    -- doesn't round-trip through the JSONB renderer the way `style` does.
    markdown   TEXT,

    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

    -- Natural PK; no separate id column because there's never more than one
    -- soul per (tenant, user) — the soul IS the identity of that pair.
    PRIMARY KEY (tenant_id, user_id)
);
