
alter table "public"."tickets" drop constraint "tickets_created_by_fkey";

alter table "public"."tickets" alter column "created_by" drop not null;
