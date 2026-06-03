

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "password" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "username" Text
--  null;

alter table "public"."cloud_accounts" alter column "account_number" set not null;

alter table "public"."cloud_accounts" alter column "cloud_provider" set not null;
