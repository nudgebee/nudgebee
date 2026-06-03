
alter table "public"."messaging_platforms" alter column "updated_by" set not null;

alter table "public"."messaging_platforms" alter column "created_by" set not null;

alter table "public"."messaging_platforms" drop constraint "messaging_platforms_platform_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."messaging_platforms" add column "platform" text
--  not null;

DELETE FROM "public"."messaging_platforms_type" WHERE "value" = 'google_chat';

DELETE FROM "public"."messaging_platforms_type" WHERE "value" = 'ms_teams';

DELETE FROM "public"."messaging_platforms_type" WHERE "value" = 'slack';

DROP TABLE "public"."messaging_platforms_type";
