-- Skill sources (#41513 / #41514): a repo + folder + branch an admin adds for the
-- organisation. Every Agent Skills folder found there becomes one organisation
-- knowledge base row (kb_type 'manual', so every skill reader keeps working),
-- linked back here by source_id and identified by its folder in the repo.
-- New table and nullable columns only: no rewrite, brief catalog locks.
CREATE TABLE IF NOT EXISTS public.llm_kb_skill_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    provider VARCHAR(32) NOT NULL DEFAULT 'github',
    repo TEXT NOT NULL,
    folder TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    visibility VARCHAR(32) NOT NULL DEFAULT 'organisation',
    account_ids UUID[] NOT NULL DEFAULT '{}',
    last_commit TEXT,
    last_synced_at TIMESTAMPTZ,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    error_message TEXT,
    -- Last sync's outcome: counts plus skipped skills and why, shown to admins.
    sync_report JSONB,
    created_by UUID,
    updated_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT llm_kb_skill_sources_tenant_id_fkey FOREIGN KEY (tenant_id)
        REFERENCES public.tenant(id) ON DELETE CASCADE,
    CONSTRAINT llm_kb_skill_sources_created_by_fkey FOREIGN KEY (created_by)
        REFERENCES public.users(id) ON DELETE SET NULL,
    CONSTRAINT llm_kb_skill_sources_updated_by_fkey FOREIGN KEY (updated_by)
        REFERENCES public.users(id) ON DELETE SET NULL,
    CONSTRAINT llm_kb_skill_sources_provider_check CHECK (provider IN ('github')),
    CONSTRAINT llm_kb_skill_sources_visibility_check CHECK (visibility IN ('organisation', 'selected_accounts')),
    CONSTRAINT llm_kb_skill_sources_status_check CHECK (status IN ('pending', 'syncing', 'active', 'error'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_skill_sources_unique
    ON public.llm_kb_skill_sources(tenant_id, provider, lower(repo), folder, branch);

COMMENT ON TABLE public.llm_kb_skill_sources IS 'Where synced Agent Skills come from; decides which accounts see its skills';
COMMENT ON COLUMN public.llm_kb_skill_sources.account_ids IS 'Accounts that see the skills when visibility = selected_accounts';

ALTER TABLE public.llm_knowledgebases ADD COLUMN IF NOT EXISTS source_id UUID;
ALTER TABLE public.llm_knowledgebases ADD COLUMN IF NOT EXISTS files JSONB;
ALTER TABLE public.llm_knowledgebases ADD COLUMN IF NOT EXISTS skill_meta JSONB;

-- No cascade: removing a source walks its skills through the normal delete path
-- so their search collections, agent links and caches are cleaned up.
ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS llm_knowledgebases_source_id_fkey;
ALTER TABLE public.llm_knowledgebases ADD CONSTRAINT llm_knowledgebases_source_id_fkey
    FOREIGN KEY (source_id) REFERENCES public.llm_kb_skill_sources(id) ON DELETE RESTRICT;

-- A synced skill is an organisation row (its source owns visibility).
ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS kb_source_org_only_check;
ALTER TABLE public.llm_knowledgebases ADD CONSTRAINT kb_source_org_only_check
    CHECK (source_id IS NULL OR account_id IS NULL);

-- A synced skill is identified by its folder in the repo, not by its name, so a
-- rename in git keeps its agent links and switches.
CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_source_path_unique
    ON public.llm_knowledgebases(source_id, (skill_meta->>'source_path'))
    WHERE source_id IS NOT NULL;

COMMENT ON COLUMN public.llm_knowledgebases.source_id IS 'Skill source this skill was synced from; NULL for hand-made and integration knowledge bases';
COMMENT ON COLUMN public.llm_knowledgebases.files IS 'Other files of an Agent Skills folder, path relative to the skill root -> text. data holds SKILL.md';
COMMENT ON COLUMN public.llm_knowledgebases.skill_meta IS 'Agent Skills details: source_path, commit, license, compatibility, metadata, allowed_tools, requirements, warnings, file list';
