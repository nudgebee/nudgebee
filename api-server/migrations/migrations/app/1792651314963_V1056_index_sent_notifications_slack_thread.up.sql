-- Matches a Slack thread message to its alert by team + thread (notifications-server events.py _alert_of_thread,
-- #40734). Build out of band on every live database first, so this is a no-op there:
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS "sent_notifications_slack_thread_idx"
--     ON "sent_notifications" ("slack_team_id", "slack_thread_id") WHERE "slack_thread_id" IS NOT NULL;
-- Elsewhere (new / on-prem) it builds here, blocking sent_notifications writes; lock_timeout fails fast rather than queue.
SET LOCAL lock_timeout = '5s';
CREATE INDEX IF NOT EXISTS "sent_notifications_slack_thread_idx"
  ON "sent_notifications" ("slack_team_id", "slack_thread_id") WHERE "slack_thread_id" IS NOT NULL;
