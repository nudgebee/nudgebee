-- The weekly review becomes one digest per tenant rather than one per cloud
-- account. Cross-account claims — a component failing in several accounts, one
-- shared mechanism behind two of them, "most volume is non-prod" — cannot be
-- made from a per-account digest, and a tenant with six accounts otherwise
-- produces six documents a week that nobody reads end to end.
--
-- Account attribution moves onto each finding instead: every entry in
-- top_classes, class_summaries and metrics.noise_classes carries its own
-- cloud_account_id and account name, so "which account is this?" stays
-- answerable per row.

-- Existing rows are account-scoped and have no tenant-level equivalent to be
-- migrated into. The generator is convergent — its gap scan refills the whole
-- lookback window on the next tick — so deleting is a regeneration, not a loss.
DELETE FROM event_analysis_digest;

ALTER TABLE event_analysis_digest
    DROP CONSTRAINT IF EXISTS event_analysis_digest_account_period_key;

DROP INDEX IF EXISTS idx_event_analysis_digest_tenant_period;

ALTER TABLE event_analysis_digest
    DROP COLUMN IF EXISTS cloud_account_id;

-- One digest per tenant-week. Also the lookup index: every read is by tenant,
-- either for one week or for the history list.
CREATE UNIQUE INDEX IF NOT EXISTS event_analysis_digest_tenant_period_key
    ON event_analysis_digest (tenant_id, period_start);

COMMENT ON TABLE event_analysis_digest IS
    'One weekly reliability review per tenant. Account attribution lives on each finding, not on the row.';
