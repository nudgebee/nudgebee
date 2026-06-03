
CREATE  INDEX "event_cloud_account_id_source_service_key_idx" on
  "public"."events" using btree ("cloud_account_id", "service_key", "source");
