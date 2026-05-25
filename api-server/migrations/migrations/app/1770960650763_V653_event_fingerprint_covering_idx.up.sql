CREATE INDEX IF NOT EXISTS "event_fingerprint_first_seen_idx"
ON "public"."events" ("cloud_account_id", "tenant", "fingerprint", "created_at");
