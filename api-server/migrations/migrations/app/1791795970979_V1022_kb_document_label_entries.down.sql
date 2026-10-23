DELETE FROM public.llm_kb_capability_entries WHERE entry_type = 'kb_document';
DROP INDEX IF EXISTS public.idx_kb_capability_entries_document;

ALTER TABLE public.llm_kb_capability_entries DROP CONSTRAINT IF EXISTS kb_capability_entries_shape_matches_type;
ALTER TABLE public.llm_kb_capability_entries
    ADD CONSTRAINT kb_capability_entries_shape_matches_type CHECK (
        (entry_type = 'platform_capability' AND name IS NOT NULL AND collection_name IS NULL AND account_id IS NULL AND document_key IS NULL)
        OR
        (entry_type = 'kb_collection' AND collection_name IS NOT NULL AND name IS NULL AND account_id IS NULL AND document_key IS NULL)
        OR
        (entry_type = 'kb_document_mark' AND document_key IS NOT NULL AND tenant_id IS NOT NULL AND kb_id IS NOT NULL AND name IS NULL AND collection_name IS NULL)
    );

ALTER TABLE public.llm_kb_capability_entries DROP CONSTRAINT IF EXISTS kb_capability_entries_type_check;
ALTER TABLE public.llm_kb_capability_entries
    ADD CONSTRAINT kb_capability_entries_type_check
    CHECK (entry_type IN ('platform_capability', 'kb_collection', 'kb_document_mark'));

ALTER TABLE public.llm_kb_capability_entries DROP COLUMN IF EXISTS source_version;
