
alter table "public"."group_roles" add column "entity_type" text
 null;

alter table "public"."group_roles" add column "entity_id" text
 null;

alter table "public"."group_roles" alter column "entity_type" set not null;

alter table "public"."group_roles" alter column "entity_id" set not null;

alter table "public"."group_roles" drop constraint "group_roles_group_id_role_key";
alter table "public"."group_roles" add constraint "group_roles_group_id_role_entity_type_entity_id_key" unique ("group_id", "role", "entity_type", "entity_id");

ALTER TABLE "public"."group_roles" ALTER COLUMN "entity_type" TYPE citext;
