-- Apply-tracking columns for alert threshold suggestions.
-- These live on the same cached row keyed (alert_rule_key, cloud_account_id);
-- the daily batch upsert (upsertSuggestion) lists explicit DO UPDATE SET columns
-- and must NOT touch these, so re-computation never clobbers apply state.
ALTER TABLE event_threshold_suggestions
    ADD COLUMN IF NOT EXISTS apply_status       TEXT,        -- null | pending | applied | failed | reverted
    ADD COLUMN IF NOT EXISTS apply_method       TEXT,        -- direct | pr
    ADD COLUMN IF NOT EXISTS applied_at         TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS applied_by         TEXT,        -- user id who applied
    ADD COLUMN IF NOT EXISTS previous_threshold NUMERIC,     -- value before apply, for revert
    ADD COLUMN IF NOT EXISTS previous_duration  INTEGER,     -- minutes before apply, for revert
    ADD COLUMN IF NOT EXISTS applied_threshold  NUMERIC,     -- value actually written (may be user-edited)
    ADD COLUMN IF NOT EXISTS resolution_id      UUID,        -- event_resolution row for the PR path
    ADD COLUMN IF NOT EXISTS apply_error        TEXT;

CREATE INDEX IF NOT EXISTS idx_ets_apply_status
    ON event_threshold_suggestions (apply_status)
    WHERE apply_status IS NOT NULL;
