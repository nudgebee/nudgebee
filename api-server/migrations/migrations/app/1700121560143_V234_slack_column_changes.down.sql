
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."slack_user" add column "slack_app_id" text
--  null;

alter table "public"."slack_installations" drop constraint "slack_installations_tenant_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."slack_installations" add column "tenant_id" uuid
--  null;
