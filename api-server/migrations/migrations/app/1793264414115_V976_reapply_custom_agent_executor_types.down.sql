-- Only the constraint goes. The backfills are not reversed: which rows held
-- 'rewoo' or 'tools' is not recorded, and current code no longer runs 'rewoo'.
ALTER TABLE public.llm_agents
	DROP CONSTRAINT IF EXISTS llm_agents_check_executortype;
