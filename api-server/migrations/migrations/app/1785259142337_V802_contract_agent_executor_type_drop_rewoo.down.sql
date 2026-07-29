-- Re-widen the CHECK to the V777 state (accept 'rewoo' again). Rows are left as
-- 'orchestrating' (still valid) — the down path only restores the constraint so a
-- rolled-back build carrying the dual-read can once more write/accept 'rewoo'.

ALTER TABLE public.llm_agents
	DROP CONSTRAINT IF EXISTS llm_agents_check_executortype;

ALTER TABLE public.llm_agents
	ADD CONSTRAINT llm_agents_check_executortype
		CHECK ("executor_type" IN ('tools', 'react', 'rewoo', 'orchestrating', 'custom'));
