
CREATE TABLE "public"."slack_channels" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "slack_bot_id" text NOT NULL, "slack_channel_id" text NOT NULL, "slack_team_id" text NOT NULL, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."slack_channels" add column "created_at" int4
 not null;

alter table "public"."slack_channels" add column "deleted_at" int4
 not null;

alter table "public"."slack_user" add column "slack_bot_user_id" text
 not null default gen_random_uuid();

ALTER TABLE "public"."slack_user" ALTER COLUMN "slack_bot_user_id" drop default;
