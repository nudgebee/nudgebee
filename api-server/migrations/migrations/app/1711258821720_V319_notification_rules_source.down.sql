
alter table "public"."notification_rules" drop constraint "notification_rules_source_fkey";

DELETE FROM "public"."notification_source_type" WHERE "value" = 'optimize';

DELETE FROM "public"."notification_source_type" WHERE "value" = 'troubleshoot';

DELETE FROM "public"."notification_source_type" WHERE "value" = 'auto_pilot';

DROP TABLE "public"."notification_source_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."notification_rules" add column "source" text
--  not null;

alter table "public"."notification_rules" rename column "is_suppressed" to "is_supressed";
