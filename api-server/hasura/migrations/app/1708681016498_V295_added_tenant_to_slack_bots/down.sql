
alter table "public"."slack_bots" alter column "tenant_id" set default gen_random_uuid();

alter table "public"."slack_bots" drop column "tenant_id" cascade
alter table "public"."slack_bots" drop column "tenant_id";
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE EXTENSION IF NOT EXISTS pgcrypto;
