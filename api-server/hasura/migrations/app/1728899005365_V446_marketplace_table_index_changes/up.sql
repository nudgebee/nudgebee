
CREATE UNIQUE INDEX "marketplace_customer_id_accout_id_product_code_index" on
  "public"."marketplace_customers" using btree ("marketplace", "customer_identifier", "provider_account_id", "product_code");
