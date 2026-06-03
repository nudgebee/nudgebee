
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resourses" add column "meta" jsonb
--  null;

alter table "public"."cloud_resourses" alter column "platform" drop not null;
alter table "public"."cloud_resourses" add column "platform" text;

alter table "public"."cloud_resourses" alter column "cost" drop not null;
alter table "public"."cloud_resourses" add column "cost" float8;

alter table "public"."cloud_resourses" alter column "size" drop not null;
alter table "public"."cloud_resourses" add column "size" text;

alter table "public"."cloud_resourses" alter column "public_ip" drop not null;
alter table "public"."cloud_resourses" add column "public_ip" text;

alter table "public"."cloud_resourses" alter column "private_ip" drop not null;
alter table "public"."cloud_resourses" add column "private_ip" text;
