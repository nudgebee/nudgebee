DROP INDEX IF EXISTS idx_ets_apply_status;

ALTER TABLE event_threshold_suggestions
    DROP COLUMN IF EXISTS apply_status,
    DROP COLUMN IF EXISTS apply_method,
    DROP COLUMN IF EXISTS applied_at,
    DROP COLUMN IF EXISTS applied_by,
    DROP COLUMN IF EXISTS previous_threshold,
    DROP COLUMN IF EXISTS previous_duration,
    DROP COLUMN IF EXISTS applied_threshold,
    DROP COLUMN IF EXISTS resolution_id,
    DROP COLUMN IF EXISTS apply_error;
