
alter table "public"."cloud_resourses" rename column "isActive" to "is_active";

alter table "public"."cloud_resourses" add column "k8s_namespace" text
 null;

alter table "public"."cloud_resourses" add column "k8s_node" text
 null;
