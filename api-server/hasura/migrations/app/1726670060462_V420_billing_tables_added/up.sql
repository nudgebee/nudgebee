
CREATE TABLE "public"."billing" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "tenant_id" uuid NOT NULL, "tier" text, "last_billed_date" timestamp, "last_billed_amount" float8 NOT NULL DEFAULT 0.00, "amount_due" float8 NOT NULL DEFAULT 0.00, PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade);
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."billing_usage_cost" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "tenant_id" uuid NOT NULL, "billing_id" uuid NOT NULL, "billing_date" timestamp NOT NULL, "service_name" text NOT NULL, "name" text NOT NULL, "units" int4 NOT NULL, "cost_per_unit" float8 NOT NULL DEFAULT 0.00, "total_cost" float8 NOT NULL DEFAULT 0.00, PRIMARY KEY ("id") , FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("billing_id") REFERENCES "public"."billing"("id") ON UPDATE cascade ON DELETE cascade);
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
CREATE TRIGGER "set_public_billing_usage_cost_updated_at"
BEFORE UPDATE ON "public"."billing_usage_cost"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_billing_usage_cost_updated_at" ON "public"."billing_usage_cost"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;
