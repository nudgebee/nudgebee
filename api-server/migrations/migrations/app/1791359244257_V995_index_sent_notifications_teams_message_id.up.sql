-- A click on a Microsoft Teams card looks up the card's stored copy by its
-- Teams message id. Build this index out of band on every live database
-- before this migration runs, so it's a no-op there:
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS "sent_notifications_teams_message_id_idx"
--     ON "sent_notifications" ("teams_message_id") WHERE "teams_message_id" IS NOT NULL;
-- Elsewhere (new and on-prem installs) it's built here, inside the migration's
-- transaction; sent_notifications is locked against writes until it finishes.
CREATE INDEX IF NOT EXISTS "sent_notifications_teams_message_id_idx"
  ON "sent_notifications" ("teams_message_id") WHERE "teams_message_id" IS NOT NULL;
