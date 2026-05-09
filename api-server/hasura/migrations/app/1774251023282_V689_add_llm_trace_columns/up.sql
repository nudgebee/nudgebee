ALTER TABLE llm_conversation_token_usage
    ADD COLUMN IF NOT EXISTS prompt_messages text,
    ADD COLUMN IF NOT EXISTS response_content text;
