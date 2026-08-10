-- The reduce stage returns the weekly review as structured sections rather than
-- one markdown blob, so the UI can render learnings, the plan and signal hygiene
-- as cards, chips and a table instead of handing prose to a generic renderer.
--
-- Nullable with no default: rows written before this column exists keep a NULL
-- briefing, and the generator's stale-shape gap scan reissues them.
ALTER TABLE event_analysis_digest
    ADD COLUMN IF NOT EXISTS briefing jsonb;

COMMENT ON COLUMN event_analysis_digest.briefing IS
    'Structured weekly review: snapshot bullets, what-broke lede, patterns, plan rows, signal hygiene. NULL on rows generated before the structured reduce stage.';
