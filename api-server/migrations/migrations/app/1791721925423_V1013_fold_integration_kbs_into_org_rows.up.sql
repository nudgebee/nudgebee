-- Integration knowledge bases become organisation knowledge bases (#40605).
--
-- Until now each (integration, linked account) pair had its own
-- llm_knowledgebases row, all sharing the integration's one collection
-- (<integration_id>_knowledge_base). This folds them into one organisation row
-- per integration (account_id NULL, visibility 'organisation'): every account in
-- the tenant sees it unless it is switched off for that account or a tenant admin
-- restricts it to selected accounts (llm_kb_account_overrides), the same model as
-- organisation SOPs from #40313.
--
-- Per integration:
--   1. one organisation row is created from the freshest per-account row;
--   2. everything that references a per-account row is moved to it first:
--        Fact/SOP marks (llm_kb_capability_entries kb_document_mark rows, and the
--        archived llm_kb_document_categories rows), and figure images where the
--        llm_kb_images table still exists (no migration creates it any more);
--      a document that every folded account marked the same way gets one
--      tenant mark instead;
--      agent mappings move too: an integration's organisation KB reaches an
--      agent only through mappings an account made, as before, so mappings are
--      now keyed by (kb_id, agent_id, account_id);
--   3. a per-account row that was switched off becomes an override that keeps
--      that account switched off;
--   4. the per-account rows are deleted.
--
-- One-way: see the .down.sql. Idempotent: a second run finds no per-account rows.
--
-- Lock window, all well under a second (a few hundred rows per tenant):
--   - llm_knowledgebases: ACCESS EXCLUSIVE for the constraint and index changes,
--     then row locks for the insert and delete;
--   - llm_kb_agent_mappings: ACCESS EXCLUSIVE while the primary key is dropped
--     and rebuilt as (kb_id, agent_id, account_id), then row locks for the moves;
--   - llm_kb_capability_entries, llm_kb_document_categories, llm_kb_images (where
--     it exists) and llm_kb_account_overrides: row locks for the moved and
--     inserted rows.
-- During a rolling deploy, llm-server pods still on the previous release fail
-- to save an agent mapping (their ON CONFLICT (kb_id, agent_id) no longer
-- matches a unique constraint) until they are replaced.

ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS kb_org_manual_only_check;

-- Organisation names stay unique per tenant for manual knowledge bases only. An
-- integration's name comes from the integration and is not unique across sources.
DROP INDEX IF EXISTS public.idx_kb_name_tenant_unique;
CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_name_tenant_unique
    ON public.llm_knowledgebases(tenant_id, name) WHERE account_id IS NULL AND integration_id IS NULL;

-- One organisation row per integration.
CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_integration_org_unique
    ON public.llm_knowledgebases(integration_id) WHERE account_id IS NULL AND integration_id IS NOT NULL;

-- 1. The organisation row, copied from the per-account row that is live and most
--    recently loaded. It is on if any account had it on; accounts that had it off
--    get an override below.
WITH ranked AS (
    SELECT kb.*,
           row_number() OVER (
               PARTITION BY kb.integration_id
               ORDER BY (kb.status <> 'archived') DESC, kb.last_loaded_at DESC NULLS LAST, kb.updated_at DESC, kb.id
           ) AS rn,
           bool_or(kb.enabled) OVER (PARTITION BY kb.integration_id) AS any_enabled
    FROM public.llm_knowledgebases kb
    WHERE kb.kb_type = 'integration' AND kb.integration_id IS NOT NULL AND kb.account_id IS NOT NULL
)
INSERT INTO public.llm_knowledgebases (
    tenant_id, account_id, name, description, data, data_format, data_filename, data_size_bytes,
    status, created_by, updated_by, created_at, updated_at, kb_type, kb_source, integration_id,
    document_count, last_loaded_at, error_message, enabled, note_category, context_tags, visibility
)
SELECT tenant_id, NULL, name, description, data, data_format, data_filename, data_size_bytes,
       status, created_by, updated_by, created_at, NOW(), kb_type, kb_source, integration_id,
       document_count, last_loaded_at, error_message, any_enabled, note_category, context_tags, 'organisation'
FROM ranked
WHERE rn = 1
ON CONFLICT (integration_id) WHERE account_id IS NULL AND integration_id IS NOT NULL DO NOTHING;

-- Every per-account row and the organisation row it folds into.
CREATE TEMP TABLE kb_fold ON COMMIT DROP AS
SELECT legacy.id AS legacy_id, org.id AS org_id, legacy.account_id, legacy.enabled, org.tenant_id
FROM public.llm_knowledgebases legacy
JOIN public.llm_knowledgebases org
  ON org.integration_id = legacy.integration_id AND org.account_id IS NULL
WHERE legacy.kb_type = 'integration' AND legacy.account_id IS NOT NULL;

-- 2. Agent mappings. Each account mapped its own row, so one organisation row
--    now carries several accounts' mappings to the same agent: the key gains
--    account_id (mapping reads and deletes already filter by it).
ALTER TABLE public.llm_kb_agent_mappings DROP CONSTRAINT IF EXISTS llm_kb_agent_mappings_pkey;
ALTER TABLE public.llm_kb_agent_mappings ADD CONSTRAINT llm_kb_agent_mappings_pkey PRIMARY KEY (kb_id, agent_id, account_id);

UPDATE public.llm_kb_agent_mappings m
SET kb_id = f.org_id
FROM kb_fold f
WHERE m.kb_id = f.legacy_id;

-- 2a. Fact/SOP marks. Account marks are unique per (account_id, document_key), so
--     moving their anchor cannot collide.
UPDATE public.llm_kb_capability_entries e
SET kb_id = f.org_id
FROM kb_fold f
WHERE e.kb_id = f.legacy_id AND e.entry_type = 'kb_document_mark';

UPDATE public.llm_kb_document_categories c
SET kb_id = f.org_id
FROM kb_fold f
WHERE c.kb_id = f.legacy_id;

--     A document that every account the knowledge base was folded from marked
--     the same way gets one tenant mark instead. Anywhere else the account marks
--     stay: a lone mark must not start applying to accounts that never set it,
--     and a tenant admin setting the mark later replaces them.
WITH folded_accounts AS (
    SELECT org_id, count(DISTINCT account_id) AS accounts FROM kb_fold GROUP BY org_id
), agreed AS (
    SELECT e.kb_id, e.tenant_id, e.document_key, min(e.note_category) AS note_category,
           min(e.created_at) AS created_at, max(e.updated_at) AS updated_at
    FROM public.llm_kb_capability_entries e
    JOIN folded_accounts fa ON fa.org_id = e.kb_id
    WHERE e.entry_type = 'kb_document_mark' AND e.account_id IS NOT NULL
    GROUP BY e.kb_id, e.tenant_id, e.document_key, fa.accounts
    HAVING count(DISTINCT e.note_category) = 1
       AND count(DISTINCT e.account_id) = fa.accounts
), promoted AS (
    INSERT INTO public.llm_kb_capability_entries
        (entry_type, tenant_id, account_id, kb_id, document_key, note_category, created_at, updated_at)
    SELECT 'kb_document_mark', tenant_id, NULL, kb_id, document_key, note_category, created_at, updated_at
    FROM agreed
    ON CONFLICT (tenant_id, document_key) WHERE entry_type = 'kb_document_mark' AND account_id IS NULL DO NOTHING
    RETURNING kb_id, document_key
)
DELETE FROM public.llm_kb_capability_entries e
USING promoted p
WHERE e.entry_type = 'kb_document_mark' AND e.account_id IS NOT NULL
  AND e.kb_id = p.kb_id AND e.document_key = p.document_key;

-- 2b. Figure images. V945 created llm_kb_images and a later revision removed
--     that migration, so the table exists only where V945 once ran. Images are
--     unique per (kb_id, page_number, figure_index): keep one per figure; the
--     per-account duplicates go with their rows.
DO $$
BEGIN
    IF to_regclass('public.llm_kb_images') IS NOT NULL THEN
        UPDATE public.llm_kb_images i
        SET kb_id = f.org_id
        FROM kb_fold f
        WHERE i.kb_id = f.legacy_id
          AND i.id IN (
              SELECT DISTINCT ON (f2.org_id, i2.page_number, i2.figure_index) i2.id
              FROM public.llm_kb_images i2
              JOIN kb_fold f2 ON f2.legacy_id = i2.kb_id
              ORDER BY f2.org_id, i2.page_number, i2.figure_index, i2.id
          )
          AND NOT EXISTS (
              SELECT 1 FROM public.llm_kb_images x
              WHERE x.kb_id = f.org_id AND x.page_number = i.page_number AND x.figure_index = i.figure_index
          );
    END IF;
END $$;

-- 3. Accounts that had their copy switched off stay switched off.
INSERT INTO public.llm_kb_account_overrides (kb_id, account_id, tenant_id, enabled, updated_at)
SELECT f.org_id, f.account_id, f.tenant_id, false, NOW()
FROM kb_fold f
WHERE NOT f.enabled
  AND EXISTS (SELECT 1 FROM public.cloud_accounts ca WHERE ca.id = f.account_id)
ON CONFLICT (kb_id, account_id) DO NOTHING;

-- 4. The per-account rows. Anything not moved above cascades with them.
DELETE FROM public.llm_knowledgebases kb
USING kb_fold f
WHERE kb.id = f.legacy_id;
