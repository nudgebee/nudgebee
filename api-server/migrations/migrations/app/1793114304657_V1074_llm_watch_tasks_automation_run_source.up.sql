-- An automation_run watch reads one run of a customer's automation until it ends: a remediation step that runs the
-- account's automation waits on it (#42459). Re-adding the CHECK scans the table once under a brief lock; it holds
-- only live and recent watches.
ALTER TABLE llm_watch_tasks DROP CONSTRAINT IF EXISTS llm_watch_tasks_source_kind_check;
ALTER TABLE llm_watch_tasks ADD CONSTRAINT llm_watch_tasks_source_kind_check CHECK (source_kind IN ('tool','sql','timer','automation_run'));
