
alter table "public"."tickets" add column "title" text
 null;

alter table "public"."tickets" add column "description" text
 null;

alter table "public"."tickets" add column "type" text
 null;

alter table "public"."tickets" alter column "title" set not null;

alter table "public"."tickets" drop constraint "tickets_severity_fkey";
