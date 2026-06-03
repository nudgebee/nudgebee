
CREATE TABLE "public"."ticket_source_table" ("value" text NOT NULL, PRIMARY KEY ("value") );

alter table "public"."tickets" rename column "type" to "source";

alter table "public"."tickets" rename column "message" to "error_message";

alter table "public"."ticket_source_table" rename to "ticket_source_type";

INSERT INTO "public"."ticket_source_type"("value") VALUES (E'recommendation');

INSERT INTO "public"."ticket_source_type"("value") VALUES (E'event');

alter table "public"."tickets"
  add constraint "tickets_source_fkey"
  foreign key ("source")
  references "public"."ticket_source_type"
  ("value") on update restrict on delete restrict;
