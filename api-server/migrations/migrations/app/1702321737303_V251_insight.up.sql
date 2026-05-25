

CREATE TABLE "public"."insight" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "title" text NOT NULL, "type" text NOT NULL, "source" text NOT NULL, "account_id" uuid NOT NULL, "tenant" uuid NOT NULL, "unique_id" text NOT NULL, "resource_id" uuid, "status" text NOT NULL, PRIMARY KEY ("id") , UNIQUE ("tenant", "account_id", "unique_id"));
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
CREATE TRIGGER "set_public_insight_updated_at"
BEFORE UPDATE ON "public"."insight"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_insight_updated_at" ON "public"."insight"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE  INDEX "insight_tenant_account_id_source_key" on
  "public"."insight" using btree ("account_id", "tenant", "source");

DROP table "public"."insights_summary";

alter table "public"."insight" add column "rule" jsonb
 null;
