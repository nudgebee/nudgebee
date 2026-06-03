ALTER TABLE public.llm_tools 
DROP CONSTRAINT if exists llm_tools_check_executortype ;


ALTER TABLE public.llm_tools 
ADD CONSTRAINT llm_tools_check_executortype 
CHECK ((executor_type = ANY (ARRAY['system'::text, 'remote'::text, 'runbook'::text, 'mcp'::text, 'container'::text])));
