
CREATE  INDEX "cloud_account_tenant_index" on
  "public"."cloud_account_score" using btree ("cloud_account_id", "tenant");
