-- Columns retrieval needs to pick a scope and rank inside it, rather than
-- returning a flat dump of the most recent messages.
--
-- Deliberately absent: reaction counts (no reaction event subscription exists,
-- so the column could only ever be zero), reply counts (derived at retrieval
-- from thread_id, no write amplification), the set of messages an answer rested
-- on (per-thread conversation state, so it lives in the event cache next to the
-- rest of it and expires with it), and embeddings (pgvector is not installed;
-- the statistical paths are a later phase).
-- Idempotent so a re-apply / out-of-band dev apply is a safe no-op.

-- Salience inputs, tagged from a keyword lexicon at ingest. Deterministic on
-- purpose: per-message model classification is the cost trap this design avoids.
ALTER TABLE messaging_channel_message
    ADD COLUMN IF NOT EXISTS is_decision boolean NOT NULL DEFAULT false;

ALTER TABLE messaging_channel_message
    ADD COLUMN IF NOT EXISTS topic text;

-- Platform user ids referenced in the text, for "what did @john say about X".
ALTER TABLE messaging_channel_message
    ADD COLUMN IF NOT EXISTS people_mentioned jsonb;

-- Thread reads are the common path now that an in-thread mention is scoped to
-- its thread. The recency index cannot serve these: thread_id is not a prefix
-- of its column list.
CREATE INDEX IF NOT EXISTS idx_messaging_channel_message_thread
    ON messaging_channel_message (tenant_id, platform, team_id, channel_id, thread_id, posted_at DESC);

-- "What did <person> say about <topic>" filters by author within a channel.
CREATE INDEX IF NOT EXISTS idx_messaging_channel_message_author
    ON messaging_channel_message (tenant_id, platform, team_id, channel_id, author_id, posted_at DESC);

-- Per-channel retrieval overrides. A busy alerts channel and a quiet design
-- channel need different windows and caps; every knob resolves per-channel
-- first and falls back to the process default when the key is absent.
ALTER TABLE messaging_channel_watch
    ADD COLUMN IF NOT EXISTS settings jsonb;
