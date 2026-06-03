

CREATE TABLE "public"."notification_channel_account_mappings" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "tenant_id" uuid NOT NULL, "platform" text NOT NULL, "team_id" text NOT NULL, "channel_id" text NOT NULL, "account_id" uuid NOT NULL, "metadata" text NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid, "updated_by" uuid, PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE cascade ON DELETE set null, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE cascade ON DELETE set null);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."notification_channel_account_mappings" add constraint "notification_channel_account_mappings_channel_id_key" unique ("channel_id");

alter table "public"."notification_channel_account_mappings" drop constraint "notification_channel_account_mappings_channel_id_key";
alter table "public"."notification_channel_account_mappings" add constraint "notification_channel_account_mappings_platform_team_id_channel_id_key" unique ("platform", "team_id", "channel_id");
