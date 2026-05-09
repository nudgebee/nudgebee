-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."sent_notifications" add column "account_id" uuid
--  null;

ALTER TABLE "public"."sent_notifications" DROP COLUMN "account_id";
