
CREATE TABLE "public"."slack_installations" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "client_id" text NOT NULL, "app_id" text NOT NULL, "enterprise_id" text, "enterprise_name" text, "enterprise_url" text, "team_id" text, "team_name" text, "bot_token" text, "bot_id" text, "bot_user_id" text, "bot_scopes" text, "user_id" text NOT NULL, "user_token" text, "user_scopes" text, "incoming_webhook_url" text, "incoming_webhook_channel" text, "incoming_webhook_channel_id" text, "incoming_webhook_configuration_url" text, "is_enterprise_install" bool NOT NULL DEFAULT false, "token_type" text, "installed_at" timestamp NOT NULL DEFAULT now(), "bot_refresh_token" text, "bot_token_expires_at" timestamp, "user_refresh_token" text, "user_token_expires_at" timestamp, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."slack_bots" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "client_id" text NOT NULL, "app_id" text NOT NULL, "enterprise_id" text, "enterprise_name" text, "team_id" text, "team_name" text, "bot_token" text, "bot_id" text, "bot_user_id" text, "bot_scopes" text, "is_enterprise_install" bool NOT NULL DEFAULT false, "installed_at" timestamp NOT NULL DEFAULT now(), "bot_refresh_token" text, "bot_token_expires_at" text, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."slack_oauth_states" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "state" text NOT NULL, "expire_at" timestamp NOT NULL, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."slack_user" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" int4 NOT NULL, "deleted_at" int4 NOT NULL, "slack_user_id" text NOT NULL, "auth_user_id" text, "secret" text NOT NULL, "slack_channel_id" text NOT NULL, "tenant_id" text, "employee_id" text, "slack_team_id" text NOT NULL, PRIMARY KEY ("id") , UNIQUE ("slack_user_id", "deleted_at"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;
