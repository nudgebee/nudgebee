
CREATE TABLE "public"."messaging_platforms" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, "tenant_id" uuid NOT NULL, "username" text, "client_id" text NOT NULL, "app_id" text, "team_id" text, "team_name" text, "token" text NOT NULL, "token_expires_at" timestamp, "scopes" text, "refresh_token" text, "refresh_token_expires_at" timestamp, "bot_id" text, PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action);
CREATE OR REPLACE FUNCTION "public"."set_current_timestamp_updated_at"()
RETURNS TRIGGER AS $$
DECLARE
  _new record;
BEGIN
  _new := NEW;
  _new."updated_at" = NOW();
  RETURN _new;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER "set_public_messaging_platforms_updated_at"
BEFORE UPDATE ON "public"."messaging_platforms"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_messaging_platforms_updated_at" ON "public"."messaging_platforms"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."messaging_platforms" add column "channels" json
 null;
