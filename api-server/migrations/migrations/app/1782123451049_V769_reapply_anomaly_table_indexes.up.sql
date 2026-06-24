-- Re-apply the `anomaly` hot-path indexes from V751 (1780917680820).
--
-- That migration was authored with a timestamp BELOW the high-water mark on the
-- test DB (whose golang-migrate version had been advanced past it by a
-- dev-snapshot clone), so `migrate up` silently skipped it there: the indexes
-- exist on prod but never landed on test. golang-migrate is forward-only and
-- will never revisit a below-mark version, so the only fix is to re-issue the
-- statements under a fresh, above-mark version.
--
-- Every statement is idempotent (IF NOT EXISTS / IF EXISTS): a no-op on tiers
-- where V751 already applied (prod), the real fix on tiers where it was skipped
-- (test). See the original 1780917680820_V751_anomaly_table_indexes for the
-- per-index query rationale.

CREATE INDEX IF NOT EXISTS idx_anomaly_account_type
    ON public.anomaly (account_id, anomaly_type);

CREATE INDEX IF NOT EXISTS idx_anomaly_account_evaluated_at
    ON public.anomaly (account_id, evaluated_at DESC);

CREATE INDEX IF NOT EXISTS idx_anomaly_open_account_type_name
    ON public.anomaly (account_id, anomaly_type, name)
    WHERE anomaly_status = 'OPEN';

-- Superseded by idx_anomaly_open_account_type_name (which adds account_id).
DROP INDEX IF EXISTS public.idx_anomaly_status_type;
