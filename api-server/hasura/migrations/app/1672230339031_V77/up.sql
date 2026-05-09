
alter table "public"."cloud_resourses" drop column "private_ip" cascade;

alter table "public"."cloud_resourses" drop column "public_ip" cascade;

alter table "public"."cloud_resourses" drop column "size" cascade;

alter table "public"."cloud_resourses" drop column "cost" cascade;

alter table "public"."cloud_resourses" drop column "platform" cascade;

alter table "public"."cloud_resourses" add column "meta" jsonb
 null;
