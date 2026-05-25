
UPDATE
    llm_conversation_messages
SET
    updated_at = created_at + interval '15 minutes'
WHERE
    updated_at - created_at > interval '1 hour'
    AND status = 'COMPLETED';
