DROP INDEX IF EXISTS idx_messaging_channel_message_author;
DROP INDEX IF EXISTS idx_messaging_channel_message_thread;

ALTER TABLE messaging_channel_message DROP COLUMN IF EXISTS people_mentioned;
ALTER TABLE messaging_channel_message DROP COLUMN IF EXISTS topic;
ALTER TABLE messaging_channel_message DROP COLUMN IF EXISTS is_decision;

ALTER TABLE messaging_channel_watch DROP COLUMN IF EXISTS settings;
