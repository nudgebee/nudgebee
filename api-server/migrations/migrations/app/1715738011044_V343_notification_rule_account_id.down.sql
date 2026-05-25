
alter table "public"."notification_rules" drop constraint "notification_rules_account_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."notification_rules" add column "account_id" uuid
--  null;
