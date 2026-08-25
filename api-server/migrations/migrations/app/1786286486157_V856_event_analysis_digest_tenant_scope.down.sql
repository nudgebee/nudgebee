-- Restores the account-scoped shape. Tenant-scoped rows carry no single account
-- and cannot be rewritten as per-account digests, so they are dropped and left
-- for the generator's gap scan to refill.
DELETE FROM event_analysis_digest;

DROP INDEX IF EXISTS event_analysis_digest_tenant_period_key;

ALTER TABLE event_analysis_digest
    ADD COLUMN IF NOT EXISTS cloud_account_id uuid NOT NULL;

ALTER TABLE event_analysis_digest
    ADD CONSTRAINT event_analysis_digest_account_period_key
    UNIQUE (cloud_account_id, period_start);

CREATE INDEX IF NOT EXISTS idx_event_analysis_digest_tenant_period
    ON event_analysis_digest (tenant_id, period_start DESC);
