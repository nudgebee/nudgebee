
alter table "public"."recommendation" alter column "resource_name" set not null;

alter table "public"."recommendation" alter column "note" set not null;

alter table "public"."recommendation" alter column "size" set not null;

alter table "public"."recommendation" alter column "cpu_utilization" set not null;

alter table "public"."recommendation" alter column "resource_type" set not null;

alter table "public"."cloud_resourses" alter column "updated_by" set not null;

alter table "public"."cloud_resourses" alter column "resourse_id" set not null;

alter table "public"."cloud_resourses" alter column "name" set not null;

alter table "public"."cloud_resourses" alter column "public_ip" set not null;

alter table "public"."cloud_resourses" alter column "private_ip" set not null;

alter table "public"."cloud_resourses" alter column "cost" set not null;

alter table "public"."cloud_resourses" alter column "resourse_created_on" set not null;

alter table "public"."cloud_resourses" alter column "created_by" set not null;

alter table "public"."cloud_resourses" alter column "type" set not null;

alter table "public"."cloud_resourses" alter column "status" set not null;

alter table "public"."cloud_resourses" alter column "size" set not null;
