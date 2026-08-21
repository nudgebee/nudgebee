ALTER TABLE messaging_channel_watch DROP COLUMN IF EXISTS channel_key;
DROP INDEX IF EXISTS idx_messaging_channel_message_posted_at;
DROP INDEX IF EXISTS idx_messaging_channel_message_fts;
DROP INDEX IF EXISTS idx_messaging_channel_message_recent;
DROP TABLE IF EXISTS messaging_channel_message;
