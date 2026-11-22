-- Delivery history lists a send's latest attempt to each destination, found by its dispatch_id.
-- sent_notifications keeps 14 days of rows, so this plain build is brief; it blocks writes to the table
-- while it runs. Where that pause is too long, build it out of band first, and this is a no-op there:
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS "sent_notifications_dispatch_idx"
--     ON "sent_notifications" ("dispatch_id") WHERE "dispatch_id" IS NOT NULL;
CREATE INDEX IF NOT EXISTS "sent_notifications_dispatch_idx"
  ON "sent_notifications" ("dispatch_id") WHERE "dispatch_id" IS NOT NULL;
