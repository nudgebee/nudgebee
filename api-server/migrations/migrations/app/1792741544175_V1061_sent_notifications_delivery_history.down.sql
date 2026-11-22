DROP INDEX IF EXISTS "sent_notifications_created_at_idx";
DROP INDEX IF EXISTS "sent_notifications_destination_message_idx";
DROP INDEX IF EXISTS "sent_notifications_tenant_fingerprint_idx";
DROP INDEX IF EXISTS "sent_notifications_tenant_created_idx";

-- Rows with no fingerprint are history rows only; nothing reads them to decide what to send.
DELETE FROM "public"."sent_notifications" WHERE "fingerprint" IS NULL;
ALTER TABLE "public"."sent_notifications" ALTER COLUMN "fingerprint" SET NOT NULL;

ALTER TABLE "public"."sent_notifications"
  DROP COLUMN IF EXISTS "routed_by",
  DROP COLUMN IF EXISTS "error_code",
  DROP COLUMN IF EXISTS "reason",
  DROP COLUMN IF EXISTS "status",
  DROP COLUMN IF EXISTS "thread_id",
  DROP COLUMN IF EXISTS "message_id",
  DROP COLUMN IF EXISTS "destination_name",
  DROP COLUMN IF EXISTS "destination",
  DROP COLUMN IF EXISTS "team_id",
  DROP COLUMN IF EXISTS "platform",
  DROP COLUMN IF EXISTS "event_id",
  DROP COLUMN IF EXISTS "title",
  DROP COLUMN IF EXISTS "source",
  DROP COLUMN IF EXISTS "kind",
  DROP COLUMN IF EXISTS "attempt",
  DROP COLUMN IF EXISTS "dispatch_id";
