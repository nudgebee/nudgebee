


CREATE TABLE "public"."roles" ("value" citext NOT NULL, "display_name" text, PRIMARY KEY ("value") );

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'business_unit_admin', null);

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'project_admin', null);

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'business_unit_update', null);

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'business_unit_create', null);

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'business_unit_delete', null);

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'business_unit_select', null);

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'project_update', null);

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'project_create', null);

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'project_delete', null);

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'project_select', null);

CREATE TABLE "public"."user_roles" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "role" citext NOT NULL, "user_id" uuid NOT NULL, "entity_type" citext NOT NULL, "entity_id" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("role") REFERENCES "public"."roles"("value") ON UPDATE restrict ON DELETE restrict, UNIQUE ("role", "user_id", "entity_type", "entity_id"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'tenant_admin', null);

alter table "public"."user_roles" add column "created_at" timestamp
 null default now();

alter table "public"."user_roles" add column "updated_at" timestamp
 null default now();

alter table "public"."user_roles" add column "created_by" uuid
 not null;

alter table "public"."user_roles"
  add constraint "user_roles_user_id_fkey"
  foreign key ("user_id")
  references "public"."users"
  ("id") on update restrict on delete restrict;

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'user', null);

alter table "public"."user_auths" add column "accessed_at" timestamp
 null default now();

alter table "public"."user_auths" alter column "credential" drop not null;

alter table "public"."user_auths" alter column "expires_at" drop not null;

alter table "public"."users" add constraint "users_username_key" unique ("username");

alter table "public"."businessunit_users" drop column "tenant_user" cascade;

INSERT INTO "public"."roles"("value", "display_name") VALUES (E'tenant_select', null);
