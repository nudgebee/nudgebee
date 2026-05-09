
alter table "public"."slack_user" alter column "slack_bot_user_id" set default gen_random_uuid();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."slack_user" add column "slack_bot_user_id" text
--  not null default gen_random_uuid();

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."slack_channels" add column "deleted_at" int4
--  not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."slack_channels" add column "created_at" int4
--  not null;

DROP TABLE "public"."slack_channels";
