ALTER TABLE llm_conversation_token_usage
    DROP COLUMN IF EXISTS prompt_messages,
    DROP COLUMN IF EXISTS response_content;
