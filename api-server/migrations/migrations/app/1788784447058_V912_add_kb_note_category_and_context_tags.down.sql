ALTER TABLE llm_knowledgebases
    DROP COLUMN IF EXISTS note_category,
    DROP COLUMN IF EXISTS context_tags;
