-- Delivery history on sent_notifications: one row per attempt to send one message to one destination.
-- Adds nullable columns only and rewrites no rows. ADD COLUMN takes a brief exclusive lock and waits behind
-- open sessions instead of failing on a timeout, so it cannot fail an upgrade.
-- On every live database, run this out of band first (columns, then indexes built concurrently), so every
-- statement below is a no-op there:
--   ALTER TABLE "sent_notifications" ADD COLUMN IF NOT EXISTS "dispatch_id" uuid, ... (the columns below);
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS "sent_notifications_tenant_created_idx"
--     ON "sent_notifications" ("tenant_id", "created_at" DESC);
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS "sent_notifications_tenant_fingerprint_idx"
--     ON "sent_notifications" ("tenant_id", "fingerprint", "created_at" DESC);
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS "sent_notifications_destination_message_idx"
--     ON "sent_notifications" ("destination", "message_id") WHERE "message_id" IS NOT NULL;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS "sent_notifications_created_at_idx"
--     ON "sent_notifications" ("created_at");
-- Elsewhere (new and on-prem installs) the table holds a day or two of rows, so each index builds here in
-- milliseconds.
ALTER TABLE "public"."sent_notifications"
  ADD COLUMN IF NOT EXISTS "dispatch_id" uuid,
  ADD COLUMN IF NOT EXISTS "attempt" smallint,
  ADD COLUMN IF NOT EXISTS "kind" text,
  ADD COLUMN IF NOT EXISTS "source" text,
  ADD COLUMN IF NOT EXISTS "title" text,
  ADD COLUMN IF NOT EXISTS "event_id" uuid,
  ADD COLUMN IF NOT EXISTS "platform" text,
  ADD COLUMN IF NOT EXISTS "team_id" text,
  ADD COLUMN IF NOT EXISTS "destination" text,
  ADD COLUMN IF NOT EXISTS "destination_name" text,
  ADD COLUMN IF NOT EXISTS "message_id" text,
  ADD COLUMN IF NOT EXISTS "thread_id" text,
  ADD COLUMN IF NOT EXISTS "status" text,
  ADD COLUMN IF NOT EXISTS "reason" text,
  ADD COLUMN IF NOT EXISTS "error_code" text,
  ADD COLUMN IF NOT EXISTS "routed_by" jsonb;

-- Summaries, reports and emails have no alert behind them.
ALTER TABLE "public"."sent_notifications" ALTER COLUMN "fingerprint" DROP NOT NULL;

CREATE INDEX IF NOT EXISTS "sent_notifications_tenant_created_idx"
  ON "sent_notifications" ("tenant_id", "created_at" DESC);
CREATE INDEX IF NOT EXISTS "sent_notifications_tenant_fingerprint_idx"
  ON "sent_notifications" ("tenant_id", "fingerprint", "created_at" DESC);
CREATE INDEX IF NOT EXISTS "sent_notifications_destination_message_idx"
  ON "sent_notifications" ("destination", "message_id") WHERE "message_id" IS NOT NULL;
CREATE INDEX IF NOT EXISTS "sent_notifications_created_at_idx"
  ON "sent_notifications" ("created_at");
