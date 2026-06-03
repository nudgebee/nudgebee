
alter table "public"."cloud_resourses" alter column "size" drop not null;

alter table "public"."cloud_resourses" alter column "status" drop not null;

alter table "public"."cloud_resourses" alter column "type" drop not null;

alter table "public"."cloud_resourses" alter column "created_by" drop not null;

alter table "public"."cloud_resourses" alter column "resourse_created_on" drop not null;

alter table "public"."cloud_resourses" alter column "cost" drop not null;

alter table "public"."cloud_resourses" alter column "private_ip" drop not null;

alter table "public"."cloud_resourses" alter column "public_ip" drop not null;

alter table "public"."cloud_resourses" alter column "name" drop not null;

alter table "public"."cloud_resourses" alter column "resourse_id" drop not null;

alter table "public"."cloud_resourses" alter column "updated_by" drop not null;

alter table "public"."recommendation" alter column "resource_type" drop not null;

alter table "public"."recommendation" alter column "cpu_utilization" drop not null;

alter table "public"."recommendation" alter column "size" drop not null;

alter table "public"."recommendation" alter column "note" drop not null;

alter table "public"."recommendation" alter column "resource_name" drop not null;
