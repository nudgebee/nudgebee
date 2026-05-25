
CREATE TABLE "public"."notifications_delivery_mode_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."notifications_delivery_mode_type"("value") VALUES (E'real_time');

INSERT INTO "public"."notifications_delivery_mode_type"("value") VALUES (E'batch');

INSERT INTO "public"."notifications_delivery_mode_type"("value") VALUES (E'suppress');

CREATE TABLE "public"."notifications_frequency_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."notifications_frequency_type"("value") VALUES (E'hourly');

INSERT INTO "public"."notifications_frequency_type"("value") VALUES (E'daily');

alter table "public"."notification_rules" add column "delivery_mode" text
 not null default 'real_time';

alter table "public"."notification_rules" add column "frequency" text
 null;

alter table "public"."notification_rules" add column "severity_levels" json
 null;

ALTER TABLE "public"."notification_rules" ALTER COLUMN "delivery_mode" drop default;
alter table "public"."notification_rules" alter column "delivery_mode" drop not null;

alter table "public"."notification_rules"
  add constraint "notification_rules_delivery_mode_fkey"
  foreign key ("delivery_mode")
  references "public"."notifications_delivery_mode_type"
  ("value") on update set null on delete set null;

alter table "public"."notification_rules"
  add constraint "notification_rules_frequency_fkey"
  foreign key ("frequency")
  references "public"."notifications_frequency_type"
  ("value") on update set null on delete set null;
