-- Organisation-wide (tenant-owned) knowledge bases, phase 1 (#40313).
-- Spec: llm/llm-server/docs/org-level-knowledge-spec.md
--
-- An organisation knowledge base is a row with no account_id. Its visibility is
-- 'organisation' (every account in the tenant, minus accounts that switched it
-- off) or 'selected_accounts' (only the accounts switched on in
-- llm_kb_account_overrides). Account rows keep visibility 'account'.
--
-- DDL only, no backfill: every existing row keeps its account_id and takes the
-- 'account' default, so nothing changes until a row with no account is written.
-- Phase 1 covers manual knowledge bases; integration rows keep one row per account.
--
-- Lock window: brief ACCESS EXCLUSIVE locks on llm_knowledgebases and
-- llm_kb_capability_entries (a few hundred rows each); the CHECKs scan those rows.

ALTER TABLE public.llm_knowledgebases ALTER COLUMN account_id DROP NOT NULL;

ALTER TABLE public.llm_knowledgebases
    ADD COLUMN IF NOT EXISTS visibility VARCHAR(32) NOT NULL DEFAULT 'account';

ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS kb_visibility_check;
ALTER TABLE public.llm_knowledgebases ADD CONSTRAINT kb_visibility_check
    CHECK (visibility IN ('account', 'organisation', 'selected_accounts'));

-- An account row has an account; an organisation row has none.
ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS kb_visibility_owner_check;
ALTER TABLE public.llm_knowledgebases ADD CONSTRAINT kb_visibility_owner_check
    CHECK ((visibility = 'account') = (account_id IS NOT NULL));

-- Phase 1: integration knowledge bases stay one row per account.
ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS kb_org_manual_only_check;
ALTER TABLE public.llm_knowledgebases ADD CONSTRAINT kb_org_manual_only_check
    CHECK (account_id IS NOT NULL OR integration_id IS NULL);

-- kb_name_account_unique (account_id, name) stays as it is: NULL account_ids never
-- conflict, so it governs account rows only. Organisation names are unique per tenant.
CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_name_tenant_unique
    ON public.llm_knowledgebases(tenant_id, name) WHERE account_id IS NULL;

COMMENT ON COLUMN public.llm_knowledgebases.account_id IS 'Owning account; NULL for an organisation (tenant-owned) knowledge base';
COMMENT ON COLUMN public.llm_knowledgebases.visibility IS 'account (owned by account_id), organisation (every account in the tenant unless switched off), or selected_accounts (only accounts switched on in llm_kb_account_overrides)';

-- Per-account restriction and switch-off for organisation knowledge bases.
--   visibility = organisation:      a row with enabled = false hides it from that account.
--   visibility = selected_accounts: only accounts with a row where enabled = true see it.
CREATE TABLE IF NOT EXISTS public.llm_kb_account_overrides (
    kb_id UUID NOT NULL,
    account_id UUID NOT NULL,
    tenant_id UUID NOT NULL,
    enabled BOOLEAN NOT NULL,
    updated_by UUID,
    updated_at TIMESTAMP DEFAULT NOW() NOT NULL,

    CONSTRAINT llm_kb_account_overrides_pkey PRIMARY KEY (kb_id, account_id),
    CONSTRAINT llm_kb_account_overrides_kb_id_fkey FOREIGN KEY (kb_id)
        REFERENCES public.llm_knowledgebases(id) ON DELETE CASCADE,
    CONSTRAINT llm_kb_account_overrides_account_id_fkey FOREIGN KEY (account_id)
        REFERENCES public.cloud_accounts(id) ON DELETE CASCADE,
    CONSTRAINT llm_kb_account_overrides_tenant_id_fkey FOREIGN KEY (tenant_id)
        REFERENCES public.tenant(id) ON DELETE CASCADE,
    CONSTRAINT llm_kb_account_overrides_updated_by_fkey FOREIGN KEY (updated_by)
        REFERENCES public.users(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_kb_account_overrides_account_id ON public.llm_kb_account_overrides(account_id);

COMMENT ON TABLE public.llm_kb_account_overrides IS 'Per-account switch for organisation knowledge bases: hides one from an account (organisation visibility) or shows it to one (selected_accounts visibility)';

-- Fact/SOP marks on an organisation knowledge base are set once for the tenant
-- (account_id NULL). An account-level mark on the same document overrides it.
ALTER TABLE public.llm_kb_capability_entries DROP CONSTRAINT IF EXISTS kb_capability_entries_shape_matches_type;
ALTER TABLE public.llm_kb_capability_entries ADD CONSTRAINT kb_capability_entries_shape_matches_type CHECK (
    (entry_type = 'platform_capability' AND name IS NOT NULL AND collection_name IS NULL AND account_id IS NULL AND document_key IS NULL)
    OR
    (entry_type = 'kb_collection' AND collection_name IS NOT NULL AND name IS NULL AND account_id IS NULL AND document_key IS NULL)
    OR
    (entry_type = 'kb_document_mark' AND document_key IS NOT NULL AND tenant_id IS NOT NULL AND kb_id IS NOT NULL AND name IS NULL AND collection_name IS NULL)
);

-- idx_kb_capability_entries_document_mark (account_id, document_key) stays the
-- arbiter for account marks; tenant marks get their own.
CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_capability_entries_document_mark_tenant
    ON public.llm_kb_capability_entries(tenant_id, document_key)
    WHERE entry_type = 'kb_document_mark' AND account_id IS NULL;
