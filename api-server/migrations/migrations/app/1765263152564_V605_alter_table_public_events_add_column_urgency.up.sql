ALTER TABLE "public"."events" ADD COLUMN IF NOT EXISTS "urgency" text DEFAULT 'LOW';

ALTER TABLE "public"."events" DROP CONSTRAINT IF EXISTS "events_urgency_fkey";

alter table "public"."events"
  add constraint "events_urgency_fkey"
  foreign key ("urgency")
  references "public"."event_severity"
  ("value") on update restrict on delete restrict;
