-- Keep LLM spend when a conversation or message is deleted.
--
-- llm_conversation_token_usage is the record of what each LLM call cost, and
-- budgets enforce against it. Its conversation and message foreign keys were
-- ON DELETE CASCADE, so deleting a conversation also deleted its spend (about
-- 30k rows on dev since July) and budgets stopped counting it. The two foreign
-- keys are dropped: these columns only record which conversation and message a
-- call ran for, like llm_cache_lifecycle and llm_gateway_usage do. Inserts no
-- longer pay for the checks, a stale id can no longer fail an insert and lose
-- the row, and deleting a conversation no longer touches this table.
--
-- tenant_id and session_id record who the spend belongs to (budget tenant and
-- module) without the conversation. llm-server and rag-server fill them on
-- insert. The conversation delete paths stamp older rows, clear their
-- prompt/response/error text and unlink them (conversation_id and message_id
-- set to NULL) before deleting, so a delete still removes the text.
--
-- Lock window: dropping a foreign key takes ACCESS EXCLUSIVE on both tables it
-- links. All three tables are locked up front, in one statement, before any
-- ALTER, and lock_timeout makes the migration fail fast if a query or a
-- conversation delete holds one of them, instead of queueing chat traffic
-- behind it or waiting in a deadlock. They are held only for catalog-only
-- changes: dropping constraints and adding nullable columns with no default.

SET LOCAL lock_timeout = '5s';

LOCK TABLE llm_conversations, llm_conversation_messages, llm_conversation_token_usage IN ACCESS EXCLUSIVE MODE;

ALTER TABLE llm_conversation_token_usage
    DROP CONSTRAINT IF EXISTS llm_conversation_token_usage_conversation_fk,
    DROP CONSTRAINT IF EXISTS llm_conversation_token_usage_message_fk,
    ADD COLUMN IF NOT EXISTS tenant_id uuid,
    ADD COLUMN IF NOT EXISTS session_id text;

COMMENT ON COLUMN llm_conversation_token_usage.tenant_id IS
    'Tenant of the owning conversation, copied so the row still counts toward budgets after the conversation is deleted. NULL for calls that never had a conversation (system cost).';
COMMENT ON COLUMN llm_conversation_token_usage.session_id IS
    'Session of the owning conversation (decides the budget module), copied for the same reason as tenant_id.';
