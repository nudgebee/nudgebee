
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."spends" add column "exclude_aggregate" boolean
--  null default 'false';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "parent_account_id" uuid
--  null;

DELETE FROM "public"."cloud_provider_type" WHERE "value" = 'Postgres';
