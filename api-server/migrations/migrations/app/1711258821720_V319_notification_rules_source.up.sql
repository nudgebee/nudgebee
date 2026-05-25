
alter table "public"."notification_rules" rename column "is_supressed" to "is_suppressed";

alter table "public"."notification_rules" add column "source" text
 not null;

CREATE TABLE "public"."notification_source_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."notification_source_type"("value") VALUES (E'auto_pilot');

INSERT INTO "public"."notification_source_type"("value") VALUES (E'troubleshoot');

INSERT INTO "public"."notification_source_type"("value") VALUES (E'optimize');

alter table "public"."notification_rules"
  add constraint "notification_rules_source_fkey"
  foreign key ("source")
  references "public"."notification_source_type"
  ("value") on update no action on delete no action;
