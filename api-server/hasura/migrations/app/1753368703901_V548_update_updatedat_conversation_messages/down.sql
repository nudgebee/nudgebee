
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- UPDATE
--     llm_conversation_messages
-- SET
--     updated_at = created_at + interval '15 minutes'
-- WHERE
--     updated_at - created_at > interval '1 hour'
--     AND status = 'COMPLETED';
