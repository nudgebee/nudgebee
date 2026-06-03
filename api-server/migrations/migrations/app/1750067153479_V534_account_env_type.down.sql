
alter table "public"."cloud_accounts" drop constraint "cloud_accounts_account_env_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "account_env" text
--  not null default 'non_prod';

alter table "public"."account_env_type" rename to "account_type";

DELETE FROM "public"."account_type" WHERE "value" = 'non-prod';

DELETE FROM "public"."account_type" WHERE "value" = 'prod';

DROP TABLE "public"."account_type";
