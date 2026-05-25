
ALTER TABLE "public"."alert_history" ALTER COLUMN "updated_at" drop default;

ALTER TABLE "public"."alert_history" ALTER COLUMN "created_at" drop default;
