CREATE  INDEX "idx_events_tenant_account_aggregation_key" on
  "public"."events" using btree ("tenant", "cloud_account_id", "aggregation_key");
