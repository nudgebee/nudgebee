-- Add ACTION_REQUIRED status to nb_status check constraint
ALTER TABLE "public"."events" DROP CONSTRAINT "events_nb_status_check";
ALTER TABLE "public"."events" ADD CONSTRAINT "events_nb_status_check"
  CHECK (nb_status = ANY (ARRAY['OPEN'::text, 'ACKNOWLEDGED'::text, 'INVESTIGATING'::text, 'ACTION_REQUIRED'::text, 'SNOOZED'::text, 'SUPPRESSED'::text, 'DROPPED'::text, 'RESOLVED'::text, 'DUPLICATE'::text]));
