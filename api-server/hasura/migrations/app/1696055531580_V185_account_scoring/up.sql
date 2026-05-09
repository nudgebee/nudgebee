
CREATE TABLE "public"."cloud_account_score" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" time NOT NULL DEFAULT now(), "score" integer NOT NULL DEFAULT 0, "description" text, "cloud_account_id" uuid NOT NULL, "source" text, "tenant" uuid NOT NULL, PRIMARY KEY ("id") , FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"));
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
CREATE TRIGGER "set_public_cloud_account_score_updated_at"
BEFORE UPDATE ON "public"."cloud_account_score"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_cloud_account_score_updated_at" ON "public"."cloud_account_score"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE UNIQUE INDEX "cloud_account_tenant_index" on
  "public"."cloud_account_score" using btree ("cloud_account_id", "tenant");
