
CREATE TABLE "public"."anomaly" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "account_id" uuid NOT NULL, "tenant" uuid NOT NULL, "name" text NOT NULL, "namespace" text NOT NULL, "reference_value" jsonb NOT NULL, "current_value" numeric NOT NULL, "anomaly_type" text NOT NULL, "is_anomaly" boolean NOT NULL, "evaluated_at" timestamp NOT NULL, PRIMARY KEY ("id") );
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
CREATE TRIGGER "set_public_anomaly_updated_at"
BEFORE UPDATE ON "public"."anomaly"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_anomaly_updated_at" ON "public"."anomaly"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."anomaly" add column "config_id" uuid
 null;
