
CREATE TABLE "public"."cloud_resourses" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_at" timestamp NOT NULL DEFAULT now(), "updated_by" uuid NOT NULL, "resourse_id" text NOT NULL, "name" text NOT NULL, "type" text NOT NULL, "size" text NOT NULL, "status" text NOT NULL, "resourse_created_on" timestamp NOT NULL, "cost" float8 NOT NULL, "account" uuid NOT NULL, "tags" Text NOT NULL, "platform" text NOT NULL, "private_ip" text NOT NULL, "public_ip" text NOT NULL, "cloud_provider" text NOT NULL, "region" text NOT NULL, "arn" text NOT NULL, "tenant" uuid NOT NULL, "business_unit" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("business_unit") REFERENCES "public"."business_unit"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("account") REFERENCES "public"."cloud_accounts"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("cloud_provider") REFERENCES "public"."cloud_provider"("value") ON UPDATE no action ON DELETE no action);
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
CREATE TRIGGER "set_public_cloud_resourses_updated_at"
BEFORE UPDATE ON "public"."cloud_resourses"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_cloud_resourses_updated_at" ON "public"."cloud_resourses" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."funding_sources" drop column "start_date" cascade;

alter table "public"."funding_sources" add column "start_date" timestamp
 not null;
