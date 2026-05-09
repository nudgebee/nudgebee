
alter table "public"."compliance_check" drop column "region" cascade;

alter table "public"."compliance_check" alter column "frequency" drop not null;

alter table "public"."compliance_standard" rename column "name" to "compliance_name";

alter table "public"."compliance_standard" drop constraint "compliance_standard_tenant_fkey";

alter table "public"."compliance_standard" drop constraint "compliance_standard_updated_by_fkey";

alter table "public"."compliance_standard" drop constraint "compliance_standard_owner_fkey";

alter table "public"."compliance_standard" drop constraint "compliance_standard_created_by_fkey";

alter table "public"."compliance_standard" drop constraint "compliance_standard_business_unit_fkey";

alter table "public"."compliance_standard" drop column "owner" cascade;

alter table "public"."compliance_standard" drop column "project" cascade;

alter table "public"."compliance_standard" drop column "business_unit" cascade;

CREATE TABLE "public"."compliance" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "name" text NOT NULL, "description" Text NOT NULL, "reference" text, PRIMARY KEY ("id") , UNIQUE ("name"));
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
CREATE TRIGGER "set_public_compliance_updated_at"
BEFORE UPDATE ON "public"."compliance"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_compliance_updated_at" ON "public"."compliance" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."compliance_rules" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "rule_name" text NOT NULL, "description" text NOT NULL, "policy" text NOT NULL, "severity" text NOT NULL, "cloud_provider" text NOT NULL, "status" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("severity") REFERENCES "public"."compliance_severity_type"("value") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("status") REFERENCES "public"."compliance_check_status_type"("value") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("cloud_provider") REFERENCES "public"."cloud_provider_type"("value") ON UPDATE cascade ON DELETE cascade);
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
CREATE TRIGGER "set_public_compliance_rules_updated_at"
BEFORE UPDATE ON "public"."compliance_rules"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_compliance_rules_updated_at" ON "public"."compliance_rules" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."compliance_findings" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "tenant" uuid NOT NULL, "business_unit" uuid NOT NULL, "severity" text NOT NULL, "compliance_status" text NOT NULL, "cloud_provider" text NOT NULL, "description" jsonb NOT NULL, "hashcode" text NOT NULL, "resource" text NOT NULL, "compliance_standard" uuid NOT NULL, "compliance_check" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("business_unit") REFERENCES "public"."business_unit"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("severity") REFERENCES "public"."compliance_severity_type"("value") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("compliance_status") REFERENCES "public"."compliance_check_status_type"("value") ON UPDATE no action ON DELETE no action, FOREIGN KEY ("cloud_provider") REFERENCES "public"."cloud_provider_type"("value") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("compliance_standard") REFERENCES "public"."compliance"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("compliance_check") REFERENCES "public"."compliance_rules"("id") ON UPDATE cascade ON DELETE cascade);
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
CREATE TRIGGER "set_public_compliance_findings_updated_at"
BEFORE UPDATE ON "public"."compliance_findings"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_compliance_findings_updated_at" ON "public"."compliance_findings" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."cloud_accounts" add column "account_type" text
 null;
