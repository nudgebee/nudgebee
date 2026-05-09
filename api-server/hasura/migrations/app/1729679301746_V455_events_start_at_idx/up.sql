
CREATE  INDEX "events_account_starts_idx" on
  "public"."events" using btree ("cloud_account_id", "starts_at");
