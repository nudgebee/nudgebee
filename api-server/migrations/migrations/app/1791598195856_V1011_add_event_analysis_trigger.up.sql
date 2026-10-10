-- Record how each alert investigation started and, when a person started it, who.
-- trigger: automatic | runbook | on_view | rerun | notification (set by llm-server).
-- Recorded on the log-analysis row that each run claims; other stage rows stay NULL.
-- triggered_by: the user id that started it; NULL for automatic and runbook runs.
-- Rows written before this change keep NULL in both. Nullable columns with no
-- default are a catalog-only change: a brief ACCESS EXCLUSIVE lock, no rewrite.
ALTER TABLE event_log_analysis ADD COLUMN IF NOT EXISTS trigger text;
ALTER TABLE event_log_analysis ADD COLUMN IF NOT EXISTS triggered_by text;
