

DELETE FROM "public"."roles" WHERE "value" = 'tenant_select';


alter table "public"."businessunit_users"
  add constraint "businessunit_users_tenant_user_fkey"
  foreign key (tenant_user)
  references "public"."tenant_users"
  (id) on update restrict on delete restrict;
alter table "public"."businessunit_users" alter column "tenant_user" drop not null;
alter table "public"."businessunit_users" add column "tenant_user" uuid;

alter table "public"."users" drop constraint "users_username_key";

alter table "public"."user_auths" alter column "expires_at" set not null;

alter table "public"."user_auths" alter column "credential" set not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."user_auths" add column "accessed_at" timestamp
--  null default now();

DELETE FROM "public"."roles" WHERE "value" = 'user';

alter table "public"."user_roles" drop constraint "user_roles_user_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."user_roles" add column "created_by" uuid
--  not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."user_roles" add column "updated_at" timestamp
--  null default now();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."user_roles" add column "created_at" timestamp
--  null default now();

DELETE FROM "public"."roles" WHERE "value" = 'tenant_admin';

DROP TABLE "public"."user_roles";

DELETE FROM "public"."roles" WHERE "value" = 'project_select';

DELETE FROM "public"."roles" WHERE "value" = 'project_delete';

DELETE FROM "public"."roles" WHERE "value" = 'project_create';

DELETE FROM "public"."roles" WHERE "value" = 'project_update';

DELETE FROM "public"."roles" WHERE "value" = 'business_unit_select';

DELETE FROM "public"."roles" WHERE "value" = 'business_unit_delete';

DELETE FROM "public"."roles" WHERE "value" = 'business_unit_create';

DELETE FROM "public"."roles" WHERE "value" = 'business_unit_update';

DELETE FROM "public"."roles" WHERE "value" = 'project_admin';

DELETE FROM "public"."roles" WHERE "value" = 'business_unit_admin';

DROP TABLE "public"."roles";
