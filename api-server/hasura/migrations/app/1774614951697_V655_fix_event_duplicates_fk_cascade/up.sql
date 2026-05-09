-- Fix: first_event_id and previous_event_id FKs use ON DELETE SET NULL,
-- but columns are NOT NULL. When referenced events are deleted, PostgreSQL
-- tries to SET NULL and hits the NOT NULL constraint:
--   pq: null value in column "first_event_id" of relation "event_duplicates" violates not-null constraint
--
-- Fix: change to ON DELETE CASCADE so the event_duplicates row is deleted
-- when the referenced event is deleted.

-- Drop existing FK constraints
ALTER TABLE "public"."event_duplicates"
  DROP CONSTRAINT IF EXISTS "event_duplicates_first_event_id_fkey";

ALTER TABLE "public"."event_duplicates"
  DROP CONSTRAINT IF EXISTS "event_duplicates_previous_event_id_fkey";

-- Re-add with ON DELETE CASCADE
ALTER TABLE "public"."event_duplicates"
  ADD CONSTRAINT "event_duplicates_first_event_id_fkey"
  FOREIGN KEY ("first_event_id") REFERENCES "public"."events"("id")
  ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE "public"."event_duplicates"
  ADD CONSTRAINT "event_duplicates_previous_event_id_fkey"
  FOREIGN KEY ("previous_event_id") REFERENCES "public"."events"("id")
  ON UPDATE CASCADE ON DELETE CASCADE;
