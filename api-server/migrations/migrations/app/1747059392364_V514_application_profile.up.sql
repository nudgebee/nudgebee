
CREATE TABLE "public"."application_profile" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "tenant_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, "pod_name" text NOT NULL, "workload_name" text NOT NULL, "namespace" text NOT NULL, "created_by" text, "profile" jsonb NOT NULL, "source_id" text NOT NULL, "source" text NOT NULL, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."application_profile" add column "created_at" timestamp
 null default now();

alter table "public"."application_profile" add column "updated_at" timestamptz
 null default now();

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
CREATE TRIGGER "set_public_application_profile_updated_at"
BEFORE UPDATE ON "public"."application_profile"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_application_profile_updated_at" ON "public"."application_profile"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';

alter table "public"."application_profile" add column "profile_type" text
 null;

alter table "public"."application_profile" add column "profile_duration" integer
 null;

alter table "public"."application_profile" add column "profile_language" text
 null;

alter table "public"."application_profile" add column "profile_tool" text
 null;
