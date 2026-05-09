

alter table "public"."cloud_accounts" drop constraint "cloud_accounts_status_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "status" text
--  not null default 'active';

DELETE FROM "public"."cloud_account_status_types" WHERE "value" = 'inactive';

DELETE FROM "public"."cloud_account_status_types" WHERE "value" = 'disabled';

DELETE FROM "public"."cloud_account_status_types" WHERE "value" = 'active';

DROP TABLE "public"."cloud_account_status_types";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."projects" add column "ended_at" timestamp
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."projects" add column "started_at" timestamp
--  null default now();
