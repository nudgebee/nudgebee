
alter table "public"."cloud_accounts" drop constraint "cloud_accounts_sync_status_fkey";

DELETE FROM "public"."cloud_account_sync_status_type" WHERE "value" = 'Queue';

DELETE FROM "public"."cloud_account_sync_status_type" WHERE "value" = 'Running';

DELETE FROM "public"."cloud_account_sync_status_type" WHERE "value" = 'Fail';

DELETE FROM "public"."cloud_account_sync_status_type" WHERE "value" = 'Success';

DROP TABLE "public"."cloud_account_sync_status_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_accounts" add column "sync_status_message" varchar(255)
--  null;
