
CREATE TABLE "public"."ms_teams_installations" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "team_id" text, "client_id" text, "access_token" text NOT NULL, "refresh_token" text NOT NULL, "tenant_id" uuid NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid, "updated_by" uuid, PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict);
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
CREATE TRIGGER "set_public_ms_teams_installations_updated_at"
BEFORE UPDATE ON "public"."ms_teams_installations"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_ms_teams_installations_updated_at" ON "public"."ms_teams_installations"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."ms_teams_installations" add column "username" text
 null;

alter table "public"."ms_teams_installations" add column "email" text
 not null;
