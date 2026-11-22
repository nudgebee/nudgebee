-- Who set up a background watch: NULL for a person (through a chat), otherwise the NudgeBee feature that owns it,
-- e.g. 'remediation' for a remediation follow-up's wait. NudgeBee's own watches have their own cap, are not merged
-- with a person's, end without a chat summary, and a person cannot cancel them.
-- Adding a nullable column without a default is a catalog-only change: no rewrite, no long lock.
ALTER TABLE llm_watch_tasks ADD COLUMN IF NOT EXISTS origin text;

-- A timer watch (source_kind 'timer', predicate_kind 'never') observes nothing and ends EXPIRED at its deadline: a
-- NudgeBee wait such as "look again at HH:MM". Re-adding the CHECKs scans the table once under a brief lock; it holds
-- only live and recent watches.
ALTER TABLE llm_watch_tasks DROP CONSTRAINT IF EXISTS llm_watch_tasks_source_kind_check;
ALTER TABLE llm_watch_tasks ADD CONSTRAINT llm_watch_tasks_source_kind_check CHECK (source_kind IN ('tool','sql','timer'));
ALTER TABLE llm_watch_tasks DROP CONSTRAINT IF EXISTS llm_watch_tasks_predicate_kind_check;
ALTER TABLE llm_watch_tasks ADD CONSTRAINT llm_watch_tasks_predicate_kind_check CHECK (predicate_kind IN ('regex','substring','llm_judge','never'));
