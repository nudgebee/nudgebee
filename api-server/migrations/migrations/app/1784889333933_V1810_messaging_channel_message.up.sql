-- Retained conversation from channels Nubi is watching. One row per (tenant,
-- message): when two tenants watch the same channel each owns its own copy, so
-- one tenant's retention or forget never touches another's.
-- Platform-agnostic on purpose — provider_message_id/thread_id are Slack's ts
-- today, a message id on Teams/Discord, a message name on Google Chat.
-- Idempotent so a re-apply / out-of-band dev apply is a safe no-op.

CREATE TABLE IF NOT EXISTS messaging_channel_message (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           uuid        NOT NULL,
    platform            text        NOT NULL,
    team_id             text        NOT NULL,
    channel_id          text        NOT NULL,
    -- Canonical channel identity that travels to every downstream consumer
    -- (memory-v2 locator, rag metadata, summary key). Generated so it can never
    -- drift from its parts. NOT tenant-unique — it narrows, tenant_id authorizes.
    channel_key         text        GENERATED ALWAYS AS (platform || ':' || team_id || ':' || channel_id) STORED,
    thread_id           text,
    provider_message_id text        NOT NULL,
    author_id           text,
    author_name         text,
    message             text        NOT NULL,
    posted_at           timestamp   NOT NULL,
    created_at          timestamp   NOT NULL DEFAULT now(),
    updated_at          timestamp   NOT NULL DEFAULT now(),
    -- Tenant must be in the dedup key: without it, a second watching tenant's
    -- copy of the same message would collide and be silently dropped.
    UNIQUE (tenant_id, platform, team_id, channel_id, provider_message_id)
);

-- Mention-time recency read. Leads with tenant_id because every read is
-- tenant-scoped. channel_key narrows but never authorizes.
CREATE INDEX IF NOT EXISTS idx_messaging_channel_message_recent
    ON messaging_channel_message (tenant_id, platform, team_id, channel_id, posted_at DESC);

-- Keyword lookup over retained conversation. Exact-word matching only — this is
-- not a substitute for semantic search.
CREATE INDEX IF NOT EXISTS idx_messaging_channel_message_fts
    ON messaging_channel_message USING GIN (to_tsvector('english', message));

-- Retention sweep scans by age across all channels.
CREATE INDEX IF NOT EXISTS idx_messaging_channel_message_posted_at
    ON messaging_channel_message (posted_at);

-- Same canonical identity on the consent registry so the two join cleanly and
-- memory-v2 can later locate facts by the key it already sees here.
ALTER TABLE messaging_channel_watch
    ADD COLUMN IF NOT EXISTS channel_key text
    GENERATED ALWAYS AS (platform || ':' || team_id || ':' || channel_id) STORED;
