
DROP INDEX IF EXISTS "public"."customer_id_accout_id_product_code_index";


DROP INDEX IF EXISTS "public"."tenant_id_billing_date_service_name_name_account_id_index";

CREATE  INDEX "tenant_id, billing_date, service_name, name, account_id" on
  "public"."billing_usage_cost" using btree ("account_id", "billing_date", "name", "service_name", "tenant_id");

DROP TABLE "public"."marketplace_customers";