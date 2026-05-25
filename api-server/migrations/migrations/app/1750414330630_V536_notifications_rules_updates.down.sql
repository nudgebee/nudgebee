
alter table "public"."notification_rules" drop constraint "notification_rules_frequency_fkey";

alter table "public"."notification_rules" drop constraint "notification_rules_delivery_mode_fkey";

alter table "public"."notification_rules" alter column "delivery_mode" set not null;
alter table "public"."notification_rules" alter column "delivery_mode" set default 'real_time'::text;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."notification_rules" add column "severity_levels" json
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."notification_rules" add column "frequency" text
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."notification_rules" add column "delivery_mode" text
--  not null default 'real_time';

DELETE FROM "public"."notifications_frequency_type" WHERE "value" = 'daily';

DELETE FROM "public"."notifications_frequency_type" WHERE "value" = 'hourly';

DROP TABLE "public"."notifications_frequency_type";

DELETE FROM "public"."notifications_delivery_mode_type" WHERE "value" = 'suppress';

DELETE FROM "public"."notifications_delivery_mode_type" WHERE "value" = 'batch';

DELETE FROM "public"."notifications_delivery_mode_type" WHERE "value" = 'real_time';

DROP TABLE "public"."notifications_delivery_mode_type";
