-- The capability catalog: one polymorphic table for what EPIC #38459's
-- SOP/runbook agentification work needs, plus the per-document Fact/SOP marks
-- #38589 added, keyed by entry_type:
--
--   'platform_capability' — the platform's real, deployed capabilities
--   (agent tools in llm-server/tools/ + workflow tasks in runbook-server),
--   with narrow/pass_through/unclassified scope classification. Written by
--   llm-server (tools/core/capability_catalog.go) at startup. Global, one
--   row per capability name — not account/tenant-scoped.
--
--   'kb_collection' — what one knowledge-base collection offers, rolled up
--   from its chunks: which kinds (sop / knowledge), shapes, and real platform
--   capabilities the labelled chunks carry. Written by rag-server
--   (documents/processing.py refresh_kb_collection_summary) after ingest,
--   prune and backfill. One row per collection; collection names are
--   UUID-derived (kb_<kb id>, <integration id>_knowledge_base), so a row is
--   not scoped to an account or tenant and is shared by every KB row that
--   maps to the collection.
--
--   'kb_document_mark' — a USER's Fact/SOP decision about one integration
--   document, previously public.llm_kb_document_categories (#39211 / V963).
--   Written and read by llm-server (tools/core/knowledgebase_document_category.go)
--   only; rag-server never reads or writes this entry type. One row per
--   (account_id, document_key) — see that file for what document_key means
--   and why it is account-wide, not per knowledge base.
--
-- A kb_collection row is DERIVED DATA — a cache of the labels in the Qdrant
-- payload — and never a marker. The classification labels (kind, shape,
-- capabilities) live on every chunk's vector payload under `metadata`, which is
-- also what RAG's metadata_filter matches at query time, and "is this chunk
-- already classified" is answered from that payload alone. An earlier revision
-- of this table kept one row per chunk (account_id, chunk_id) as the marker; two
-- stores that must agree kept disagreeing (rows for chunks that never landed,
-- rows outliving a pruned or deleted collection, a row written for a chunk
-- whose payload carried no label), so the marker was moved into the one store
-- that cannot disagree with itself. The rollup can always be rebuilt from the
-- payload, and the rag-server backfill does so for any collection that has
-- labelled chunks but no row.
--
-- A kb_document_mark row is the OPPOSITE of derived: it is the one thing in
-- this whole table a human decided, and nothing recomputes it. Two tables
-- doing conceptually adjacent things (this table's kind/shape is an automatic
-- suggestion, kb_document_mark is the user's override) were confusing side by
-- side, so #39211's table is folded in here rather than left as a lookalike.
-- Nothing about how it behaves changes: same key, same CHECK values, same
-- kb_id cascade, same account-wide scope — only where the rows live.
--
-- The labels themselves are per SOURCE DOCUMENT — one LLM classification of the
-- whole document (the design's L0 step, doc -> shape) — and every chunk of the
-- document carries them.
--
-- Why one table: a kb_collection row's `capabilities` list is grounded against
-- this table's own platform_capability rows (rag-server's
-- get_capability_catalog() reads entry_type = 'platform_capability' from the
-- same table it writes entry_type = 'kb_collection' rows into) — the two
-- halves are read/write partners on one feature, not independent concerns.
-- kb_document_mark joins them because it is the same shape of problem (per-KB
-- classification metadata) even though its owner and trust level differ, which
-- is exactly why it stays a clearly separate entry_type rather than reusing
-- kind/shape/capabilities: a user's decision must never be read as, or
-- overwritten by, an automatic one.
--
-- rag-server reads/writes this table directly over its own metastore
-- connection rather than calling back into llm-server for the
-- platform_capability half — mirroring how it already reads
-- `integrations`/`integration_config_values` directly. No reverse-direction
-- HTTP dependency exists anywhere in this codebase; this follows that same
-- pattern instead of introducing one. rag-server never touches kb_document_mark
-- rows; only llm-server does, exactly as it only ever touched
-- llm_kb_document_categories before.
--
-- The DROP below converges a database that already holds the pre-release shape
-- of this table (one row per chunk, keyed by account_id + chunk_id). That shape
-- was applied by hand to the dev database from this PR's branch and never
-- shipped to any other environment, so on every other database it is a no-op.
-- Nothing is lost by it: platform_capability rows are rewritten by llm-server at
-- startup and kb_collection rows are rebuilt from the vector payload.
--
-- public.llm_kb_document_categories (#39211 / V963) is DELIBERATELY NOT DROPPED
-- here, even though nothing in this PR's code reads or writes it any more once
-- knowledgebase_document_category.go is repointed at kb_document_mark rows.
-- Migrations for this service run as a pre-upgrade Helm hook, strictly before
-- the new llm-server image rolls out (deploy/kubernetes/migrations); dropping
-- the table in the same migration would mean the still-running OLD llm-server
-- pods — which read/write llm_kb_document_categories directly — see it vanish
-- out from under them for however long the rolling restart takes, turning every
-- Fact/SOP mark read or write on an old pod into an error for that window. Data
-- copied below is a point-in-time snapshot for the same reason: a mark set on
-- an old pod after this migration runs and before it is replaced lands in the
-- old table only, not in the copy. That gap is bounded by one rollout and
-- accepted for a low-traffic, non-retrieval-critical write (ResolveDocumentCategories
-- and the read paths in knowledgebase_documents.go all fail open on a missing
-- row). The now-redundant table is dropped in a follow-up migration once the
-- rollout is confirmed complete; see this file's .down.sql for the reverse.
--
-- Expected lock window: negligible — a DROP and a CREATE on a table that is
-- empty or holds a few hundred rows, plus a single-digit-to-low-hundreds-row
-- INSERT ... SELECT copy of public.llm_kb_document_categories.
DROP TABLE IF EXISTS public.llm_kb_capability_entries;

CREATE TABLE IF NOT EXISTS public.llm_kb_capability_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entry_type VARCHAR(50) NOT NULL,

    -- platform_capability columns (NULL on every other entry type)
    name VARCHAR(255),
    surface VARCHAR(50),
    scope VARCHAR(50),
    description TEXT,

    -- kb_collection columns (NULL / empty on every other entry type)
    collection_name VARCHAR(255),
    kinds TEXT[] NOT NULL DEFAULT '{}',
    shapes TEXT[] NOT NULL DEFAULT '{}',

    -- kb_collection: the capabilities the collection's chunks reference.
    -- Other entry types leave it empty.
    capabilities TEXT[] NOT NULL DEFAULT '{}',

    -- kb_document_mark columns (NULL on every other entry type). Same fields,
    -- same meaning, as the former public.llm_kb_document_categories.
    tenant_id UUID,
    account_id UUID,
    kb_id UUID,
    document_key TEXT,
    note_category VARCHAR(50),
    created_by UUID,
    updated_by UUID,
    created_at TIMESTAMP DEFAULT NOW() NOT NULL,

    updated_at TIMESTAMP DEFAULT NOW() NOT NULL,

    CONSTRAINT kb_capability_entries_type_check
        CHECK (entry_type IN ('platform_capability', 'kb_collection', 'kb_document_mark')),
    CONSTRAINT kb_capability_entries_surface_check CHECK (surface IS NULL OR surface IN ('agent_tool', 'workflow_task')),
    CONSTRAINT kb_capability_entries_scope_check CHECK (scope IS NULL OR scope IN ('narrow', 'pass_through', 'unclassified')),
    CONSTRAINT kb_capability_entries_kinds_check CHECK (kinds <@ ARRAY['sop', 'knowledge']::text[]),
    CONSTRAINT kb_capability_entries_shapes_check CHECK (shapes <@ ARRAY[
        'deterministic_procedure', 'decision_tree', 'narrative_reference',
        'ui_walkthrough_business_app', 'credential_access_procedure'
    ]::text[]),
    -- Mirrors the CHECK public.llm_kb_document_categories carried on note_category.
    CONSTRAINT kb_capability_entries_note_category_check
        CHECK (note_category IS NULL OR note_category IN ('sop', 'fact')),
    -- Enforces the polymorphic shape: a row's identity columns must match its
    -- own entry_type and no other's. Stops a row from being read as one type
    -- while carrying another type's identity — in particular, stops a
    -- kb_document_mark row (a user's decision) from ever being mistaken for a
    -- kb_collection row (an automatic suggestion), or vice versa.
    CONSTRAINT kb_capability_entries_shape_matches_type CHECK (
        (entry_type = 'platform_capability' AND name IS NOT NULL AND collection_name IS NULL AND account_id IS NULL AND document_key IS NULL)
        OR
        (entry_type = 'kb_collection' AND collection_name IS NOT NULL AND name IS NULL AND account_id IS NULL AND document_key IS NULL)
        OR
        (entry_type = 'kb_document_mark' AND account_id IS NOT NULL AND document_key IS NOT NULL AND tenant_id IS NOT NULL AND kb_id IS NOT NULL AND name IS NULL AND collection_name IS NULL)
    ),

    -- Same cascade public.llm_kb_document_categories had: deleting a knowledge
    -- base takes its marks with it. Only ever populated for kb_document_mark
    -- rows, so this is a no-op for the other two entry types (NULL always
    -- satisfies a foreign key).
    CONSTRAINT kb_capability_entries_kb_id_fkey FOREIGN KEY (kb_id)
        REFERENCES public.llm_knowledgebases(id) ON DELETE CASCADE ON UPDATE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_capability_entries_name
    ON public.llm_kb_capability_entries(name) WHERE entry_type = 'platform_capability';
CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_capability_entries_collection
    ON public.llm_kb_capability_entries(collection_name) WHERE entry_type = 'kb_collection';
-- Same uniqueness public.llm_kb_document_categories enforced (account_id,
-- document_key): one mark per document per account, regardless of which
-- knowledge base it was set from.
CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_capability_entries_document_mark
    ON public.llm_kb_capability_entries(account_id, document_key) WHERE entry_type = 'kb_document_mark';
-- So the kb_id cascade above is an index lookup, not a scan of every mark —
-- same reason public.llm_kb_document_categories had its own kb_id index.
CREATE INDEX IF NOT EXISTS idx_kb_capability_entries_kb_id
    ON public.llm_kb_capability_entries(kb_id) WHERE entry_type = 'kb_document_mark';
CREATE INDEX IF NOT EXISTS idx_kb_capability_entries_type ON public.llm_kb_capability_entries(entry_type);
CREATE INDEX IF NOT EXISTS idx_kb_capability_entries_scope ON public.llm_kb_capability_entries(scope) WHERE entry_type = 'platform_capability';

-- One-time copy of every existing Fact/SOP mark. public.llm_kb_document_categories
-- was created by V963 (#39211), earlier in migration order than this file, so it
-- exists (possibly empty, on a fresh database) by the time this runs. Read-only:
-- the source table and its rows are left exactly as they were: see the header
-- comment above for why. Fresh ids: this table's identity is a surrogate UUID
-- unrelated to the source table's, so there is nothing to preserve there.
INSERT INTO public.llm_kb_capability_entries
    (entry_type, tenant_id, account_id, kb_id, document_key, note_category, created_by, updated_by, created_at, updated_at)
SELECT 'kb_document_mark', tenant_id, account_id, kb_id, document_key, note_category, created_by, updated_by, created_at, updated_at
FROM public.llm_kb_document_categories;

COMMENT ON TABLE public.llm_kb_capability_entries IS 'Polymorphic capability catalog: platform_capability rows (the platform''s real tools/tasks, global), kb_collection rows (a per-collection rollup of the chunk labels in the vector payload, derived data), and kb_document_mark rows (a user''s Fact/SOP decision about one document, formerly llm_kb_document_categories), discriminated by entry_type.';
COMMENT ON COLUMN public.llm_kb_capability_entries.entry_type IS 'platform_capability, kb_collection, or kb_document_mark — determines which of the column groups below is populated.';
COMMENT ON COLUMN public.llm_kb_capability_entries.name IS '[platform_capability] Registered capability name, e.g. kubectl_execute or k8s.workload_restart.';
COMMENT ON COLUMN public.llm_kb_capability_entries.surface IS '[platform_capability] Which registry this capability comes from: agent_tool (llm-server) or workflow_task (runbook-server).';
COMMENT ON COLUMN public.llm_kb_capability_entries.scope IS '[platform_capability] narrow (typed, bounded), pass_through (single free-text field, unbounded scope), or unclassified (present in the live catalog but not yet in llm-server''s checked-in table — never treated as narrow).';
COMMENT ON COLUMN public.llm_kb_capability_entries.collection_name IS '[kb_collection] The vector collection this row summarises: kb_<kb id> for a manual KB, <integration id>_knowledge_base for an integration KB.';
COMMENT ON COLUMN public.llm_kb_capability_entries.kinds IS '[kb_collection] Every kind a labelled chunk of the collection carries: sop (has steps) or knowledge (narrative reference). Derived from the chunk payload; never a marker.';
COMMENT ON COLUMN public.llm_kb_capability_entries.shapes IS '[kb_collection] Every full L0 structural classification a labelled chunk of the collection carries.';
COMMENT ON COLUMN public.llm_kb_capability_entries.capabilities IS '[kb_collection] Real, grounded capability names (this table''s own platform_capability rows) the collection''s chunks reference. Validated at write time — never an unverified name. Empty on platform_capability and kb_document_mark rows.';
COMMENT ON COLUMN public.llm_kb_capability_entries.document_key IS '[kb_document_mark] Stable document handle: <collection>|<scraper source id>, or <collection>|<url> when the document has no source id.';
COMMENT ON COLUMN public.llm_kb_capability_entries.kb_id IS '[kb_document_mark] Knowledge base the mark was made from; ties the mark''s lifetime to it via ON DELETE CASCADE. A mark applies account-wide, not per knowledge base.';
COMMENT ON COLUMN public.llm_kb_capability_entries.note_category IS '[kb_document_mark] sop (procedure: agents follow its steps) or fact (reference material) — a user''s decision, never derived.';
