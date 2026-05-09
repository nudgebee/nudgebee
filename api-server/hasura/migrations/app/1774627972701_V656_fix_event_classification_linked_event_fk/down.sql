-- Revert to original NO ACTION (note: this reintroduces the cleanup blocker)
ALTER TABLE "public"."event_classification"
  DROP CONSTRAINT IF EXISTS "event_classification_linked_event_id_fkey";

ALTER TABLE "public"."event_classification"
  ADD CONSTRAINT "event_classification_linked_event_id_fkey"
  FOREIGN KEY ("linked_event_id") REFERENCES "public"."events"("id");
