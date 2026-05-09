

INSERT INTO "public"."notification_source_type"("value") VALUES (E'daily_recap');

alter table "public"."notification_rules" alter column "cluster" drop not null;
