
CREATE TABLE "public"."compliance_check" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, "name" varchar NOT NULL, "description" varchar NOT NULL, "tenant" uuid NOT NULL, "business_unit" uuid NOT NULL, "frequency" bigint NOT NULL, "region" varchar NOT NULL, "severity" varchar NOT NULL, "cloud_provider" varchar NOT NULL, "check_type" varchar NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("business_unit") REFERENCES "public"."business_unit"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict);
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
CREATE TRIGGER "set_public_compliance_check_updated_at"
BEFORE UPDATE ON "public"."compliance_check"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_compliance_check_updated_at" ON "public"."compliance_check" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."compliance_standard" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, "name" varchar NOT NULL, "description" varchar NOT NULL, "tenant" uuid NOT NULL, "business_unit" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("business_unit") REFERENCES "public"."business_unit"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict);
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
CREATE TRIGGER "set_public_compliance_standard_updated_at"
BEFORE UPDATE ON "public"."compliance_standard"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_compliance_standard_updated_at" ON "public"."compliance_standard" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE "public"."compliance_standard" ALTER COLUMN "updated_at" TYPE timestamp;

alter table "public"."compliance_standard" add column "last_executed" time without time zone
 null;

ALTER TABLE "public"."compliance_standard" ALTER COLUMN "created_at" TYPE timestamp;

ALTER TABLE "public"."compliance_check" ALTER COLUMN "updated_at" TYPE timestamp;

ALTER TABLE "public"."compliance_check" ALTER COLUMN "created_at" TYPE timestamp;

alter table "public"."compliance_standard" drop column "last_executed" cascade;

alter table "public"."compliance_check" add column "last_executed" timestamp without time zone
 null;

CREATE TABLE "public"."compliance_standard_check_mappings" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "check_id" uuid NOT NULL, "standard_id" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("check_id") REFERENCES "public"."compliance_check"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("standard_id") REFERENCES "public"."compliance_standard"("id") ON UPDATE restrict ON DELETE restrict);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."compliance_check_findings" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "severity" varchar NOT NULL, "status" varchar NOT NULL, "project" uuid NOT NULL, "compliance_standard" uuid NOT NULL, "compliance_check" uuid NOT NULL, "account" uuid NOT NULL, "resource" varchar NOT NULL, "tenant" uuid NOT NULL, "business_unit" uuid NOT NULL, "created_by" uuid NOT NULL, "updated_by" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("business_unit") REFERENCES "public"."business_unit"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("compliance_check") REFERENCES "public"."compliance_check"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("compliance_standard") REFERENCES "public"."compliance_standard"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("project") REFERENCES "public"."projects"("id") ON UPDATE restrict ON DELETE restrict);
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
CREATE TRIGGER "set_public_compliance_check_findings_updated_at"
BEFORE UPDATE ON "public"."compliance_check_findings"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_compliance_check_findings_updated_at" ON "public"."compliance_check_findings" 
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."compliance_severity" ("value" varchar NOT NULL, "comment" varchar NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."compliance_severity"("value", "comment") VALUES (E'High', E'High');

INSERT INTO "public"."compliance_severity"("value", "comment") VALUES (E'Critical', E'Critical');

INSERT INTO "public"."compliance_severity"("value", "comment") VALUES (E'Medium', E'Medium');

INSERT INTO "public"."compliance_severity"("value", "comment") VALUES (E'Informational', E'Informational');

INSERT INTO "public"."compliance_severity"("value", "comment") VALUES (E'Low', E'Low');

alter table "public"."compliance_check_findings"
  add constraint "compliance_check_findings_severity_fkey"
  foreign key ("severity")
  references "public"."compliance_severity"
  ("value") on update restrict on delete restrict;

ALTER TABLE "public"."compliance_severity" ALTER COLUMN "value" TYPE text;

ALTER TABLE "public"."compliance_severity" ALTER COLUMN "comment" TYPE text;

alter table "public"."compliance_check"
  add constraint "compliance_check_severity_fkey"
  foreign key ("severity")
  references "public"."compliance_severity"
  ("value") on update restrict on delete restrict;
