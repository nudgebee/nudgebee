
alter table "public"."event_rules" alter column "id" set default gen_random_uuid();

alter table "public"."event_rules" add column "group" text
 null;
