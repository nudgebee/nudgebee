-- Opt-in registry for Nubi channel awareness: which messaging channels the bot
-- passively follows. A row is created on first enable; disabling keeps the row
-- (enabled=false + disabled_at) so consent history survives toggling.
-- Idempotent so a re-apply / out-of-band dev apply is a safe no-op.

CREATE TABLE IF NOT EXISTS messaging_channel_watch (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid        NOT NULL,
    platform       text        NOT NULL,
    team_id        text        NOT NULL,
    channel_id     text        NOT NULL,
    channel_name   text,
    enabled        boolean     NOT NULL DEFAULT true,
    retention_days integer     NOT NULL DEFAULT 30,
    created_by     text,
    created_at     timestamp   NOT NULL DEFAULT now(),
    updated_at     timestamp   NOT NULL DEFAULT now(),
    disabled_at    timestamp,
    UNIQUE (tenant_id, platform, team_id, channel_id)
);

-- Message-event ingest resolves (platform, team_id, channel_id) -> watched? on the
-- hot path (Redis-mirrored, this is the fallback); partial index keeps it tight.
CREATE INDEX IF NOT EXISTS idx_messaging_channel_watch_team_channel
    ON messaging_channel_watch (platform, team_id, channel_id) WHERE enabled;

-- Per-tenant gate consumed by the notifications-server feature_flag check; the
-- feature must exist before a tenant row can reference it.
INSERT INTO feature (value, description) VALUES
    ('CHANNEL_AWARENESS', 'Nubi passively follows opted-in messaging channels and uses the conversation as context when mentioned')
ON CONFLICT (value) DO NOTHING;
