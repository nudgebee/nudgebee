
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resourses" add column "k8s_node" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resourses" add column "k8s_namespace" text
--  null;

alter table "public"."cloud_resourses" rename column "is_active" to "isActive";
