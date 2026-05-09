
alter table "public"."cloud_resourses" add column "first_seen" timestamp
 null;

alter table "public"."cloud_resourses" add column "last_seen" timestamp
 null;

alter table "public"."cloud_resourses" add column "isActive" boolean
 null;
