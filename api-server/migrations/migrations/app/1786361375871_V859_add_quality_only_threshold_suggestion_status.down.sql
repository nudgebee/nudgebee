-- Fold quality_only rows back into 'skipped' before restoring the narrower CHECK.
UPDATE "public"."event_threshold_suggestions" SET status = 'skipped' WHERE status = 'quality_only';
ALTER TABLE "public"."event_threshold_suggestions" DROP CONSTRAINT IF EXISTS "event_threshold_suggestions_status_check";
ALTER TABLE "public"."event_threshold_suggestions" ADD CONSTRAINT "event_threshold_suggestions_status_check" CHECK (status = ANY (ARRAY['ok'::text, 'skipped'::text, 'error'::text, 'not_eligible'::text]));
