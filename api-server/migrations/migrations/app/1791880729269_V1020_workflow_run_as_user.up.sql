-- The user an automation's unattended runs (schedule, event, webhook) act as.
-- Set on create and when a version is published or made live; never by
-- pause/resume or draft edits. Backfilled with the last editor, which is who
-- every run acts as today, so existing automations behave the same.
-- Lock window: one UPDATE over workflows (a few thousand rows at most).
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS run_as_user_id uuid REFERENCES users(id) ON DELETE SET NULL;

UPDATE workflows SET run_as_user_id = COALESCE(updated_by, created_by) WHERE run_as_user_id IS NULL;
