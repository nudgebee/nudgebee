-- Fix: event_classification.linked_event_id FK has NO ACTION on delete,
-- which blocks the event cleanup job from deleting old events.
-- The column is nullable, so ON DELETE SET NULL is the correct behavior:
-- when a linked event is deleted, just null out the reference.

ALTER TABLE "public"."event_classification"
  DROP CONSTRAINT IF EXISTS "event_classification_linked_event_id_fkey";

ALTER TABLE "public"."event_classification"
  ADD CONSTRAINT "event_classification_linked_event_id_fkey"
  FOREIGN KEY ("linked_event_id") REFERENCES "public"."events"("id")
  ON UPDATE CASCADE ON DELETE SET NULL;
