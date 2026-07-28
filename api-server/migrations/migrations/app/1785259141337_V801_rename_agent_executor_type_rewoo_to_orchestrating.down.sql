-- Revert 'orchestrating' back to 'rewoo' and restore the original CHECK.
-- Order matters: revert the rows first so narrowing the constraint doesn't
-- reject any still-'orchestrating' row.

UPDATE public.llm_agents
	SET executor_type = 'rewoo'
	WHERE executor_type = 'orchestrating';

ALTER TABLE public.llm_agents
	DROP CONSTRAINT IF EXISTS llm_agents_check_executortype;

ALTER TABLE public.llm_agents
	ADD CONSTRAINT llm_agents_check_executortype
		CHECK ("executor_type" IN ('tools', 'react', 'rewoo', 'custom'));
