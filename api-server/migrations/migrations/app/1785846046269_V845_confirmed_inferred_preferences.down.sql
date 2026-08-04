-- Dropping these loses the record of which explicit rows were kept inferences
-- rather than typed settings. The rows themselves and their injection
-- behaviour are unaffected; they simply become unreviewable again.
ALTER TABLE llm_memory_preferences
    DROP COLUMN IF EXISTS confirmed_by,
    DROP COLUMN IF EXISTS confirmed_at,
    DROP COLUMN IF EXISTS origin;
