
INSERT INTO "public"."recommendation_status_type"("value") VALUES (E'Archive');

alter table "public"."recommendation_status_type" add column "description" text
 null;
