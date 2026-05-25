
alter table "public"."notification_rule_mappings" drop constraint "notification_rule_mappings_platform_fkey";

DELETE FROM "public"."notification_platform_types" WHERE "value" = 'ms_teams';

DELETE FROM "public"."notification_platform_types" WHERE "value" = 'slack';

DELETE FROM "public"."notification_platform_types" WHERE "value" = 'email';

DROP TABLE "public"."notification_platform_types";

ALTER TABLE "public"."notification_rules" ALTER COLUMN "updated_at" drop default;

ALTER TABLE "public"."notification_rules" ALTER COLUMN "created_at" drop default;

DROP TABLE "public"."notification_rule_mappings";

DROP TABLE "public"."notification_rules";
