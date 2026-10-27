ALTER TABLE llm_watch_tasks DROP CONSTRAINT IF EXISTS llm_watch_tasks_predicate_kind_check;
ALTER TABLE llm_watch_tasks ADD CONSTRAINT llm_watch_tasks_predicate_kind_check CHECK (predicate_kind IN ('regex','substring','llm_judge'));
ALTER TABLE llm_watch_tasks DROP CONSTRAINT IF EXISTS llm_watch_tasks_source_kind_check;
ALTER TABLE llm_watch_tasks ADD CONSTRAINT llm_watch_tasks_source_kind_check CHECK (source_kind IN ('tool','sql'));
ALTER TABLE llm_watch_tasks DROP COLUMN IF EXISTS origin;
