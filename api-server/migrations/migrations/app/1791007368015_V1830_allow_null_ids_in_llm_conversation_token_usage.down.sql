-- Restoring NOT NULL fails if background-job rows with NULL ids exist; remove
-- them first. Deliberately not done here, as that would delete usage data.
ALTER TABLE public.llm_conversation_token_usage
    ALTER COLUMN conversation_id SET NOT NULL,
    ALTER COLUMN account_id      SET NOT NULL;
