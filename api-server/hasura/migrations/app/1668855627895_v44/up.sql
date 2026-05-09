
alter table "public"."cloud_resourses" drop constraint "cloud_resourses_business_unit_fkey";

alter table "public"."cloud_resourses" drop column "business_unit" cascade;

alter table "public"."cloud_resourses" alter column "platform" drop not null;
