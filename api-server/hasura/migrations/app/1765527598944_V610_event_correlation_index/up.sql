
create index events_fingerprint_account_starts_idx ON events(fingerprint, cloud_account_id, starts_at DESC);
