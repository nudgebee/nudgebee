-- Organisation-wide custom agents and tools, schema (#41073).
-- Same user rules as organisation knowledge bases (#40313): a tenant admin
-- shares an agent or tool with every account (or selected accounts), and an
-- account admin can switch it off for their account.
--
-- Ownership:
--   visibility = 'account'            owned by its single installation row, as before.
--   visibility = 'organisation'       no installation row; every account of tenant_id,
--                                     minus accounts with an override enabled = false.
--   visibility = 'selected_accounts'  no installation row; only accounts with an
--                                     override enabled = true.
-- Per-account switches live in their own tables, not in the installation tables,
-- because llm_agents_installation also holds per-account extension rows.
--
-- Existing rows take the 'account' default, so nothing changes until a row
-- with another visibility is written. Also deletes custom agents that have no
-- installation row: no read can reach them (every read joins installation).
--
-- Lock window: brief ACCESS EXCLUSIVE locks on llm_agents and llm_tools (a few
-- hundred rows each); the CHECKs and unique indexes scan those rows.

ALTER TABLE public.llm_agents
    ADD COLUMN IF NOT EXISTS visibility VARCHAR(32) NOT NULL DEFAULT 'account';
ALTER TABLE public.llm_tools
    ADD COLUMN IF NOT EXISTS visibility VARCHAR(32) NOT NULL DEFAULT 'account';

ALTER TABLE public.llm_agents DROP CONSTRAINT IF EXISTS llm_agents_visibility_check;
ALTER TABLE public.llm_agents ADD CONSTRAINT llm_agents_visibility_check
    CHECK (visibility IN ('account', 'organisation', 'selected_accounts'));
ALTER TABLE public.llm_tools DROP CONSTRAINT IF EXISTS llm_tools_visibility_check;
ALTER TABLE public.llm_tools ADD CONSTRAINT llm_tools_visibility_check
    CHECK (visibility IN ('account', 'organisation', 'selected_accounts'));

-- An organisation row is scoped by its tenant alone.
ALTER TABLE public.llm_agents DROP CONSTRAINT IF EXISTS llm_agents_org_tenant_check;
ALTER TABLE public.llm_agents ADD CONSTRAINT llm_agents_org_tenant_check
    CHECK (visibility = 'account' OR tenant_id IS NOT NULL);
ALTER TABLE public.llm_tools DROP CONSTRAINT IF EXISTS llm_tools_org_tenant_check;
ALTER TABLE public.llm_tools ADD CONSTRAINT llm_tools_org_tenant_check
    CHECK (visibility = 'account' OR tenant_id IS NOT NULL);

-- Organisation names are unique per tenant, ignoring case. A clash with an
-- account row is checked by llm-server under a lock (account rows may already
-- repeat a name across accounts).
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_agents_org_name_unique
    ON public.llm_agents(tenant_id, lower(name)) WHERE visibility <> 'account';
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_tools_org_name_unique
    ON public.llm_tools(tenant_id, lower(name)) WHERE visibility <> 'account';

COMMENT ON COLUMN public.llm_agents.visibility IS 'account (owned by its llm_agents_installation row), organisation (every account in tenant_id unless switched off) or selected_accounts (only accounts switched on in llm_agent_account_overrides)';
COMMENT ON COLUMN public.llm_tools.visibility IS 'account (owned by its llm_tools_installation row), organisation (every account in tenant_id unless switched off) or selected_accounts (only accounts switched on in llm_tool_account_overrides)';

-- Per-account switches for organisation agents and tools, as llm_kb_account_overrides:
--   visibility = organisation:      a row with enabled = false hides it from that account.
--   visibility = selected_accounts: only accounts with a row where enabled = true see it.
CREATE TABLE IF NOT EXISTS public.llm_agent_account_overrides (
    agent_id UUID NOT NULL,
    account_id UUID NOT NULL,
    tenant_id UUID NOT NULL,
    enabled BOOLEAN NOT NULL,
    updated_by UUID,
    updated_at TIMESTAMP DEFAULT NOW() NOT NULL,

    CONSTRAINT llm_agent_account_overrides_pkey PRIMARY KEY (agent_id, account_id),
    CONSTRAINT llm_agent_account_overrides_agent_id_fkey FOREIGN KEY (agent_id)
        REFERENCES public.llm_agents(id) ON DELETE CASCADE,
    CONSTRAINT llm_agent_account_overrides_account_id_fkey FOREIGN KEY (account_id)
        REFERENCES public.cloud_accounts(id) ON DELETE CASCADE,
    CONSTRAINT llm_agent_account_overrides_tenant_id_fkey FOREIGN KEY (tenant_id)
        REFERENCES public.tenant(id) ON DELETE CASCADE,
    CONSTRAINT llm_agent_account_overrides_updated_by_fkey FOREIGN KEY (updated_by)
        REFERENCES public.users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_llm_agent_account_overrides_account_id
    ON public.llm_agent_account_overrides(account_id);
COMMENT ON TABLE public.llm_agent_account_overrides IS 'Per-account switch for organisation custom agents: hides one from an account (organisation visibility) or shows it to one (selected_accounts visibility)';

CREATE TABLE IF NOT EXISTS public.llm_tool_account_overrides (
    tool_id UUID NOT NULL,
    account_id UUID NOT NULL,
    tenant_id UUID NOT NULL,
    enabled BOOLEAN NOT NULL,
    updated_by UUID,
    updated_at TIMESTAMP DEFAULT NOW() NOT NULL,

    CONSTRAINT llm_tool_account_overrides_pkey PRIMARY KEY (tool_id, account_id),
    CONSTRAINT llm_tool_account_overrides_tool_id_fkey FOREIGN KEY (tool_id)
        REFERENCES public.llm_tools(id) ON DELETE CASCADE,
    CONSTRAINT llm_tool_account_overrides_account_id_fkey FOREIGN KEY (account_id)
        REFERENCES public.cloud_accounts(id) ON DELETE CASCADE,
    CONSTRAINT llm_tool_account_overrides_tenant_id_fkey FOREIGN KEY (tenant_id)
        REFERENCES public.tenant(id) ON DELETE CASCADE,
    CONSTRAINT llm_tool_account_overrides_updated_by_fkey FOREIGN KEY (updated_by)
        REFERENCES public.users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_llm_tool_account_overrides_account_id
    ON public.llm_tool_account_overrides(account_id);
COMMENT ON TABLE public.llm_tool_account_overrides IS 'Per-account switch for organisation custom tools: hides one from an account (organisation visibility) or shows it to one (selected_accounts visibility)';

-- Custom agents with no installation row are unreachable (on dev: 49 rows,
-- including the one with no tenant). Nothing references llm_agents by foreign
-- key. Idempotent: a second run finds no rows.
DELETE FROM public.llm_agents a
WHERE a.type = 'custom'
  AND a.visibility = 'account'
  AND NOT EXISTS (
      SELECT 1 FROM public.llm_agents_installation i WHERE i.agent_id = a.id::text
  );
