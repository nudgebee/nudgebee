-- Re-applies V777/V779 (#33515) where they never ran, and retires the `tools`
-- executor for custom agents (#39973).
--
-- The Atlas cutover baselined V777 and V779 as applied without running them
-- (atlas_schema_revisions applied=0 on test), so databases that crossed the
-- cutover kept 'rewoo' rows and have no executor_type CHECK at all.
--
-- Custom 'tools' agents ran the single-tool-call prompt planner. Nothing writes
-- that value for a custom agent any more; ReAct runs the same tools in a loop.
-- Built-in agents keep 'tools' (AgentPlannerTypeTool), so the CHECK still allows it.
--
-- NOT VALID: new writes are checked, but an install holding some other legacy
-- value still upgrades instead of failing its one-shot migration Job.
-- Lock window: a few hundred rows; the UPDATEs and the constraint swap are instant.

UPDATE public.llm_agents
	SET executor_type = 'orchestrating'
	WHERE executor_type = 'rewoo';

UPDATE public.llm_agents
	SET executor_type = 'react'
	WHERE executor_type = 'tools' AND "type" = 'custom';

ALTER TABLE public.llm_agents
	DROP CONSTRAINT IF EXISTS llm_agents_check_executortype;

ALTER TABLE public.llm_agents
	ADD CONSTRAINT llm_agents_check_executortype
		CHECK ("executor_type" IN ('tools', 'react', 'orchestrating', 'custom')) NOT VALID;
