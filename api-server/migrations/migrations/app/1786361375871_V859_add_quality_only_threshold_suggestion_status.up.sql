-- Allow status='quality_only' on event_threshold_suggestions: a health verdict computed from
-- firing history alone, for rules whose metric can't be fetched (no numeric threshold suggestion).
ALTER TABLE "public"."event_threshold_suggestions" DROP CONSTRAINT IF EXISTS "event_threshold_suggestions_status_check";
ALTER TABLE "public"."event_threshold_suggestions" ADD CONSTRAINT "event_threshold_suggestions_status_check" CHECK (status = ANY (ARRAY['ok'::text, 'skipped'::text, 'error'::text, 'not_eligible'::text, 'quality_only'::text]));
