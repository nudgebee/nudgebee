
alter table "public"."application_group" alter column "updated_at" drop not null;

alter table "public"."application_group" alter column "updated_by" drop not null;
