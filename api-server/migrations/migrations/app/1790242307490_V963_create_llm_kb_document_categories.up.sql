-- Per-document Fact/SOP marks for a knowledge base.
--
-- The documents themselves live in the rag-server vector collection, not here: their
-- point ids are content hashes, so a re-sync replaces them and a mark keyed on one would
-- evaporate. document_key is the stable handle instead: "<collection>|<source id>", where
-- the source id is the id the scraper stamped (Confluence page_id, ServiceNow sys_id).
-- It survives an edit, a rename and a move. A document that carries no source id falls
-- back to "<collection>|<url>". A document with neither cannot be marked; manual
-- knowledge bases are categorised on llm_knowledgebases.note_category instead.
--
-- A mark is identified by (account_id, document_key), NOT by knowledge base. One
-- integration collection is shared by every knowledge base row of that integration
-- (kbCollectionName keys it on integration_id), so a retrieval hit cannot say which row
-- it came from, and the same page reached through two knowledge bases is one document.
--
-- kb_id records which knowledge base the mark was made from, and ties the row's lifetime
-- to it: ON DELETE CASCADE means deleting the KB takes its marks with it. Whole-tenant
-- deletion already deletes llm_knowledgebases (DeleteTenant in
-- api-server/services/tenant/service.go), so those cascade too.
--
-- Expected lock window: negligible — a CREATE TABLE and an index on an empty table.
CREATE TABLE IF NOT EXISTS llm_kb_document_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
    tenant_id UUID NOT NULL,
    account_id UUID NOT NULL,
    kb_id UUID NOT NULL,
    document_key TEXT NOT NULL,
    note_category VARCHAR(50) NOT NULL,
    created_by UUID,
    updated_by UUID,
    created_at TIMESTAMP DEFAULT NOW() NOT NULL,
    updated_at TIMESTAMP DEFAULT NOW() NOT NULL,

    CONSTRAINT kb_doc_category_kb_id_fkey FOREIGN KEY (kb_id)
        REFERENCES llm_knowledgebases(id) ON DELETE CASCADE ON UPDATE RESTRICT,
    CONSTRAINT kb_doc_category_unique UNIQUE (account_id, document_key),
    CONSTRAINT kb_doc_category_check CHECK (note_category IN ('sop', 'fact'))
);

-- kb_doc_category_unique already indexes (account_id, document_key), which is how both
-- the Documents list and retrieval read this table. kb_id needs its own so the cascade
-- from a knowledge base delete is an index lookup, not a scan of every mark.
CREATE INDEX IF NOT EXISTS llm_kb_document_categories_kb_id_idx ON llm_kb_document_categories (kb_id);

COMMENT ON TABLE llm_kb_document_categories IS 'Per-document Fact/SOP marks for knowledge base documents stored in the vector index';
COMMENT ON COLUMN llm_kb_document_categories.document_key IS 'Stable document handle: <collection>|<scraper source id>, or <collection>|<url> when the document has no source id';
COMMENT ON COLUMN llm_kb_document_categories.kb_id IS 'Knowledge base the mark was made from; ties the mark lifetime to it. A mark applies account-wide, not per knowledge base';
COMMENT ON COLUMN llm_kb_document_categories.note_category IS 'sop (procedure: agents follow its steps) or fact (reference material)';
