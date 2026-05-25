
CREATE TABLE "public"."cloud_accounts" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "cloud_provider" text NOT NULL, "account_number" text NOT NULL, "account_name" text NOT NULL, "created_at" timestamptz NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_at" timestamptz NOT NULL DEFAULT now(), "updated_by" uuid NOT NULL, "billing_source" text NOT NULL, "start_date" timestamptz, "account_email" text, "tenant" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action);
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
CREATE TRIGGER "set_public_cloud_accounts_updated_at"
BEFORE UPDATE ON "public"."cloud_accounts"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_cloud_accounts_updated_at" ON "public"."cloud_accounts" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."project_accounts" ("project_id" uuid NOT NULL, "account_id" uuid NOT NULL, "id" bigserial NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("project_id") REFERENCES "public"."projects"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict);
