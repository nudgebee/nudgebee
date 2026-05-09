
ALTER TABLE "public"."group_roles" ALTER COLUMN "entity_type" TYPE text;

alter table "public"."group_roles" drop constraint "group_roles_group_id_role_entity_type_entity_id_key";
alter table "public"."group_roles" add constraint "group_roles_group_id_role_key" unique ("group_id", "role");

alter table "public"."group_roles" alter column "entity_id" drop not null;

alter table "public"."group_roles" alter column "entity_type" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."group_roles" add column "entity_id" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."group_roles" add column "entity_type" text
--  null;
