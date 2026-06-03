alter table "public"."cloud_resourses" alter column external_resource_id drop not null;
alter table "public"."cloud_resourses" alter column external_resource_id type  text;
alter table "public"."cloud_resourses" drop constraint "cloud_resourses_optscale_resource_id_key";
