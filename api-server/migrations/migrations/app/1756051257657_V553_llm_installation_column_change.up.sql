ALTER TABLE public.llm_agents_installation ALTER COLUMN agent_id TYPE text USING agent_id::text;

ALTER TABLE public.llm_agents_installation ADD COLUMN IF NOT EXISTS  additional_instructions text;

ALTER TABLE public.llm_agents_installation ADD COLUMN IF NOT EXISTS  tools json;
