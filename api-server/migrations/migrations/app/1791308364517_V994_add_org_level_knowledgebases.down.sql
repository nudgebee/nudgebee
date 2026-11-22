-- Reverts V994. Refuses while organisation knowledge bases or tenant-level marks
-- exist: restoring NOT NULL would fail on them, and dropping them here would
-- delete customer content.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.llm_knowledgebases WHERE account_id IS NULL) THEN
        RAISE EXCEPTION 'V994 down: organisation knowledge bases exist (account_id IS NULL); delete or reassign them first';
    END IF;
    IF EXISTS (SELECT 1 FROM public.llm_kb_capability_entries WHERE entry_type = 'kb_document_mark' AND account_id IS NULL) THEN
        RAISE EXCEPTION 'V994 down: tenant-level document marks exist; delete them first';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_kb_capability_entries_document_mark_tenant;

ALTER TABLE public.llm_kb_capability_entries DROP CONSTRAINT IF EXISTS kb_capability_entries_shape_matches_type;
ALTER TABLE public.llm_kb_capability_entries ADD CONSTRAINT kb_capability_entries_shape_matches_type CHECK (
    (entry_type = 'platform_capability' AND name IS NOT NULL AND collection_name IS NULL AND account_id IS NULL AND document_key IS NULL)
    OR
    (entry_type = 'kb_collection' AND collection_name IS NOT NULL AND name IS NULL AND account_id IS NULL AND document_key IS NULL)
    OR
    (entry_type = 'kb_document_mark' AND account_id IS NOT NULL AND document_key IS NOT NULL AND tenant_id IS NOT NULL AND kb_id IS NOT NULL AND name IS NULL AND collection_name IS NULL)
);

DROP TABLE IF EXISTS public.llm_kb_account_overrides;

DROP INDEX IF EXISTS idx_kb_name_tenant_unique;
ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS kb_org_manual_only_check;
ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS kb_visibility_owner_check;
ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS kb_visibility_check;
ALTER TABLE public.llm_knowledgebases DROP COLUMN IF EXISTS visibility;
ALTER TABLE public.llm_knowledgebases ALTER COLUMN account_id SET NOT NULL;

COMMENT ON COLUMN public.llm_knowledgebases.account_id IS NULL;
