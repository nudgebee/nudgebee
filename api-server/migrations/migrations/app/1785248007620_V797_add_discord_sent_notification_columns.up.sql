-- Discord finding dedup/threading: repeat findings reply to the original message
-- instead of posting fresh top-level messages (parity with Slack threads).
ALTER TABLE sent_notifications ADD COLUMN IF NOT EXISTS discord_channel_id text;
ALTER TABLE sent_notifications ADD COLUMN IF NOT EXISTS discord_message_id text;
