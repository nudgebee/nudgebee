-- Fourth entry_type of llm_kb_capability_entries (V977): 'kb_document' — the
-- classifier's labels (kind, shape, capabilities) for ONE page of an integration
-- knowledge base, so Postgres can answer "what is this page" without a Qdrant
-- read (EPIC #38459, docs/kb-label-ownership-spec.md).
--
--   collection_name  the integration KB collection (<integration id>_knowledge_base)
--   document_key     <collection>|<scraper source id | url> — the SAME grammar and the
--                    same recipe as kb_document_mark (llm-server DocumentKey; rag-server
--                    rag.core.documents.document_key, kept identical by a shared
--                    golden-vector test). Manual KBs (kb_<id>) have no per-page key and
--                    are not stored here.
--   kinds/shapes/capabilities  one kind, one shape, the grounded capability names
--   source_version   the scraper's version of the page the labels were made from;
--                    NULL = seeded from existing Qdrant labels, version unknown
--
-- No tenant/account/kb_id: like a kb_collection row it is shared by every KB row
-- mapping to the collection, and its lifetime follows the collection (rag-server
-- deletes it with the collection), not a knowledge base row.
--
-- Additive and rollout-safe: every existing reader and writer filters by its own
-- entry_type, so old pods ignore these rows. Nothing writes them in this release
-- until the rag-server seed job is enabled.
--
-- Expected lock window: negligible — two CHECK swaps on a table of a few hundred
-- rows (each re-validates those rows) and an ADD COLUMN without a default.
ALTER TABLE public.llm_kb_capability_entries
    ADD COLUMN IF NOT EXISTS source_version TEXT;

ALTER TABLE public.llm_kb_capability_entries DROP CONSTRAINT IF EXISTS kb_capability_entries_type_check;
ALTER TABLE public.llm_kb_capability_entries
    ADD CONSTRAINT kb_capability_entries_type_check
    CHECK (entry_type IN ('platform_capability', 'kb_collection', 'kb_document_mark', 'kb_document'));

ALTER TABLE public.llm_kb_capability_entries DROP CONSTRAINT IF EXISTS kb_capability_entries_shape_matches_type;
ALTER TABLE public.llm_kb_capability_entries
    ADD CONSTRAINT kb_capability_entries_shape_matches_type CHECK (
        (entry_type = 'platform_capability' AND name IS NOT NULL AND collection_name IS NULL AND account_id IS NULL AND document_key IS NULL)
        OR
        (entry_type = 'kb_collection' AND collection_name IS NOT NULL AND name IS NULL AND account_id IS NULL AND document_key IS NULL)
        OR
        (entry_type = 'kb_document_mark' AND document_key IS NOT NULL AND tenant_id IS NOT NULL AND kb_id IS NOT NULL AND name IS NULL AND collection_name IS NULL)
        OR
        (entry_type = 'kb_document' AND collection_name IS NOT NULL AND document_key IS NOT NULL AND name IS NULL AND account_id IS NULL AND tenant_id IS NULL AND kb_id IS NULL)
    );

CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_capability_entries_document
    ON public.llm_kb_capability_entries(collection_name, document_key) WHERE entry_type = 'kb_document';

COMMENT ON COLUMN public.llm_kb_capability_entries.source_version IS '[kb_document] The scraper''s version of the page the labels were made from (Confluence page version, ServiceNow sys_updated_on, or a hash of the scraped text). NULL when seeded from existing Qdrant labels.';
