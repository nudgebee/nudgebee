-- Phase 2 (contract) of the ReWoo → Orchestrating executor_type cleanup (#33515).
-- V777 (expand) widened the CHECK to accept BOTH 'rewoo' and 'orchestrating' and
-- backfilled existing rows. Now that every writer emits 'orchestrating' (the
-- AgentPlannerTypeOrchestrating constant since #33565), drop 'rewoo' from the
-- allowed set.
--
-- Self-healing: re-run the backfill FIRST so the narrowed CHECK cannot fail on a
-- straggler 'rewoo' row (e.g. one written by an old pod mid-rollout before #33565).
-- This runs as a pre-upgrade hook, so it completes before the dual-read-free code
-- (this same PR) starts serving reads.

UPDATE public.llm_agents
	SET executor_type = 'orchestrating'
	WHERE executor_type = 'rewoo';

ALTER TABLE public.llm_agents
	DROP CONSTRAINT IF EXISTS llm_agents_check_executortype;

ALTER TABLE public.llm_agents
	ADD CONSTRAINT llm_agents_check_executortype
		CHECK ("executor_type" IN ('tools', 'react', 'orchestrating', 'custom'));
