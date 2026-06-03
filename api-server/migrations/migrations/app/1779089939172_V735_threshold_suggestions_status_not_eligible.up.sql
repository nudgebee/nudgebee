ALTER TABLE "public"."event_threshold_suggestions" DROP CONSTRAINT "event_threshold_suggestions_status_check";
ALTER TABLE "public"."event_threshold_suggestions" ADD CONSTRAINT "event_threshold_suggestions_status_check" CHECK (status = ANY (ARRAY['ok'::text, 'skipped'::text, 'error'::text, 'not_eligible'::text]));
