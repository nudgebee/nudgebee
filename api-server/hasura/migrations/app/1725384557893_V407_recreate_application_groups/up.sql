
DROP table if exists "public"."applications_grouping";

DROP table if exists  "public"."application_groups_applications";

DROP table if exists  "public"."application_groups";

CREATE TABLE "public"."application_group" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "name" text NOT NULL, "description" Text, "tenant_id" uuid NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "created_by" uuid NOT NULL, "updated_at" timestamp NOT NULL DEFAULT now(), "updated_by" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("created_by") REFERENCES "public"."users"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("updated_by") REFERENCES "public"."users"("id") ON UPDATE cascade ON DELETE cascade, UNIQUE ("name", "tenant_id"));COMMENT ON TABLE "public"."application_group" IS E'This table contains groups definition';
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

CREATE TRIGGER "set_public_application_group_updated_at"
BEFORE UPDATE ON "public"."application_group"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_application_group_updated_at" ON "public"."application_group"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."application_group_mapping" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "tenant_id" uuid NOT NULL, "account_id" uuid NOT NULL, "group_id" uuid NOT NULL, "namespace_name" Text NOT NULL, "workload_name" text NOT NULL, "workload_kind" text NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("group_id") REFERENCES "public"."application_group"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, UNIQUE ("id"));COMMENT ON TABLE "public"."application_group_mapping" IS E'This table contains the list of applications which are part of an application_group';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."auto_pilot_task" add column "skipped_by" uuid
 null;
