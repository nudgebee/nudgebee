-- Rename the custom-agent planner value 'rewoo' -> 'orchestrating' so the
-- persisted string matches the intent-based Go constant
-- (AgentPlannerTypeOrchestrating). Part of the ReWoo naming cleanup (#33515).
--
-- Expand-migrate step (contract is deferred to a later migration):
--   1. Widen the CHECK to accept BOTH the old and new values so that, during a
--      rolling deploy, pods still running the old build can keep writing 'rewoo'
--      without violating the constraint.
--   2. Backfill existing rows to the new value. New reads dual-map any straggler
--      'rewoo' rows (written by old pods after this backfill) to Orchestrating,
--      so leaving 'rewoo' in the CHECK is safe.

ALTER TABLE public.llm_agents
	DROP CONSTRAINT IF EXISTS llm_agents_check_executortype;

ALTER TABLE public.llm_agents
	ADD CONSTRAINT llm_agents_check_executortype
		CHECK ("executor_type" IN ('tools', 'react', 'rewoo', 'orchestrating', 'custom'));

UPDATE public.llm_agents
	SET executor_type = 'orchestrating'
	WHERE executor_type = 'rewoo';
