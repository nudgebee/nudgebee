DROP INDEX IF EXISTS idx_messaging_channel_watch_team_channel;
DROP TABLE IF EXISTS messaging_channel_watch;
DELETE FROM feature_flag WHERE feature_id = 'CHANNEL_AWARENESS';
DELETE FROM feature WHERE value = 'CHANNEL_AWARENESS';
