
INSERT INTO "public"."event_status"("value") VALUES (E'CLOSED');

alter table "public"."events" add column "status" text
 null;

alter table "public"."events"
  add constraint "events_status_fkey"
  foreign key ("status")
  references "public"."event_status"
  ("value") on update restrict on delete restrict;
