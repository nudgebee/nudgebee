-- Restore the ON DELETE CASCADE foreign keys. Rows unlinked while they were
-- dropped have NULL ids and stay valid. The constraints are added NOT VALID:
-- a row written meanwhile with an id whose conversation or message is gone
-- would fail a full validation.

SET LOCAL lock_timeout = '5s';

LOCK TABLE llm_conversations, llm_conversation_messages, llm_conversation_token_usage IN ACCESS EXCLUSIVE MODE;

ALTER TABLE llm_conversation_token_usage
    DROP CONSTRAINT IF EXISTS llm_conversation_token_usage_conversation_fk,
    DROP CONSTRAINT IF EXISTS llm_conversation_token_usage_message_fk,
    ADD CONSTRAINT llm_conversation_token_usage_conversation_fk
        FOREIGN KEY (conversation_id) REFERENCES llm_conversations(id) ON DELETE CASCADE NOT VALID,
    ADD CONSTRAINT llm_conversation_token_usage_message_fk
        FOREIGN KEY (message_id) REFERENCES llm_conversation_messages(id) ON DELETE CASCADE NOT VALID,
    DROP COLUMN IF EXISTS session_id,
    DROP COLUMN IF EXISTS tenant_id;
