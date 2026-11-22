DROP TABLE IF EXISTS public.llm_tool_approval_decisions;
DROP TABLE IF EXISTS public.llm_tool_approval_rule_events;
DROP TABLE IF EXISTS public.llm_tool_approval_rules;
ALTER TABLE IF EXISTS public.cloud_accounts
    DROP CONSTRAINT IF EXISTS cloud_accounts_id_tenant_unique;
