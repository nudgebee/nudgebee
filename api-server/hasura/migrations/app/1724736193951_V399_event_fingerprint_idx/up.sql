
CREATE  INDEX "event_tenant_account_fingerprint_inx" on
  "public"."events" using btree ("cloud_account_id", "tenant", "fingerprint");
