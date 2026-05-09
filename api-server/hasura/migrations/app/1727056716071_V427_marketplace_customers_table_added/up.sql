

DROP INDEX IF EXISTS "public"."tenant_id, billing_date, service_name, name, account_id";

CREATE UNIQUE INDEX "tenant_id_billing_date_service_name_name_account_id_index" on
  "public"."billing_usage_cost" using btree ("tenant_id", "billing_date", "service_name", "name", "account_id");

CREATE TABLE "public"."marketplace_customers" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "customer_identifier" text NOT NULL, "provider_account_id" text NOT NULL, "product_code" text NOT NULL, "pricing_tier" text, "entitlement_details" jsonb, "offer_identifier" text, "is_free_trial_on" boolean NOT NULL DEFAULT false, "subscription_expity" timestamp, "action" text, "status" boolean DEFAULT false, "tenant_id" uuid,  PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE SET null ON DELETE SET null);
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
CREATE TRIGGER "set_public_marketplace_customers_updated_at"
BEFORE UPDATE ON "public"."marketplace_customers"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_marketplace_customers_updated_at" ON "public"."marketplace_customers"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;


CREATE UNIQUE INDEX "customer_id_accout_id_product_code_index" on
  "public"."marketplace_customers" using btree ("customer_identifier", "provider_account_id", "product_code");
