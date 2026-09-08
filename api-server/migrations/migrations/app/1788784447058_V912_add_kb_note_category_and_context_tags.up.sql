-- Add two capture-only fields to knowledge-base notes:
--
--   note_category  — an organizational label (fact / rule / sop / policy) shown
--                    on the note. Distinct from kb_type (manual/integration),
--                    which is integration plumbing. Plain TEXT with no CHECK:
--                    the value set is product taxonomy likely to evolve, and a
--                    CHECK constraint would force a migration on every taxonomy
--                    change. Nullable — existing rows and integration KBs simply
--                    have none.
--
--   context_tags   — scope labels (e.g. 'Service: auth-svc') captured on a note.
--                    TEXT[] with an empty-array default so existing rows read a
--                    well-formed empty list. No index: tags are not used to
--                    filter the list (capture + show-back only; folding them into
--                    retrieval is a follow-up change).
--
-- Lock window: ADD COLUMN with a constant/absent default is metadata-only on
-- PG 11+ (no table rewrite), so this takes a brief ACCESS EXCLUSIVE lock and
-- returns. No backfill.
ALTER TABLE llm_knowledgebases
    ADD COLUMN IF NOT EXISTS note_category TEXT,
    ADD COLUMN IF NOT EXISTS context_tags TEXT[] NOT NULL DEFAULT '{}';

COMMENT ON COLUMN llm_knowledgebases.note_category IS
    'Organizational label for a note: fact, rule, sop, or policy. Display/scan only; does not affect routing or retrieval. Distinct from kb_type (manual/integration).';

COMMENT ON COLUMN llm_knowledgebases.context_tags IS
    'Scope labels (e.g. "Service: auth-svc") captured on a note. Not used for list filtering.';
