
alter table "public"."cloud_accounts" alter column "account_type" drop not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- update cloud_accounts
-- set agent_access_key = access_key, agent_access_secret = access_secret
-- where account_type = 'kubernetes';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "agent_synced_at" timestamp
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "agent_access_secret" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "agent_access_key" text
--  null;

alter table "public"."cloud_accounts" alter column "account_email" drop not null;
alter table "public"."cloud_accounts" add column "account_email" text;

alter table "public"."cloud_accounts" alter column "username" drop not null;
alter table "public"."cloud_accounts" add column "username" text;

alter table "public"."cloud_accounts" alter column "password" drop not null;
alter table "public"."cloud_accounts" add column "password" text;

alter table "public"."cloud_accounts" alter column "port" drop not null;
alter table "public"."cloud_accounts" add column "port" int4;
