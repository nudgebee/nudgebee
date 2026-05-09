-- Revert to original ON DELETE SET NULL (note: this reintroduces the bug)
ALTER TABLE "public"."event_duplicates"
  DROP CONSTRAINT IF EXISTS "event_duplicates_first_event_id_fkey";

ALTER TABLE "public"."event_duplicates"
  DROP CONSTRAINT IF EXISTS "event_duplicates_previous_event_id_fkey";

ALTER TABLE "public"."event_duplicates"
  ADD CONSTRAINT "event_duplicates_first_event_id_fkey"
  FOREIGN KEY ("first_event_id") REFERENCES "public"."events"("id")
  ON UPDATE SET NULL ON DELETE SET NULL;

ALTER TABLE "public"."event_duplicates"
  ADD CONSTRAINT "event_duplicates_previous_event_id_fkey"
  FOREIGN KEY ("previous_event_id") REFERENCES "public"."events"("id")
  ON UPDATE SET NULL ON DELETE SET NULL;
