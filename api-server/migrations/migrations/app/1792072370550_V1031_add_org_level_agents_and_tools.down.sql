-- Reverts the schema of V1031. Deleted orphan agents are not restored.
-- Organisation agents and tools (no installation row) would become unreachable
-- after this; delete them first if any exist.
DROP TABLE IF EXISTS public.llm_tool_account_overrides;
DROP TABLE IF EXISTS public.llm_agent_account_overrides;
DROP INDEX IF EXISTS public.idx_llm_tools_org_name_unique;
DROP INDEX IF EXISTS public.idx_llm_agents_org_name_unique;
ALTER TABLE public.llm_tools DROP CONSTRAINT IF EXISTS llm_tools_org_tenant_check;
ALTER TABLE public.llm_agents DROP CONSTRAINT IF EXISTS llm_agents_org_tenant_check;
ALTER TABLE public.llm_tools DROP CONSTRAINT IF EXISTS llm_tools_visibility_check;
ALTER TABLE public.llm_agents DROP CONSTRAINT IF EXISTS llm_agents_visibility_check;
ALTER TABLE public.llm_tools DROP COLUMN IF EXISTS visibility;
ALTER TABLE public.llm_agents DROP COLUMN IF EXISTS visibility;
