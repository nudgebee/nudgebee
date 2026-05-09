
CREATE TABLE "public"."knowledge_base" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "description" text NOT NULL, "impact" text NOT NULL, "diagnosis" text NOT NULL, "mitigation" text NOT NULL, "rule_name" text NOT NULL, PRIMARY KEY ("id") , UNIQUE ("rule_name"));
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
CREATE TRIGGER "set_public_knowledge_base_updated_at"
BEFORE UPDATE ON "public"."knowledge_base"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_knowledge_base_updated_at" ON "public"."knowledge_base"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;
