-- Background-job LLM calls have no owning conversation turn, so they have no
-- llm_conversation_messages row to point at. V765 already made conversation_id
-- and account_id nullable for exactly this reason but missed message_id, so
-- every such call still fails with 23502 and its cost row is dropped: the five
-- memory maintenance jobs (session_distill, patterns_extract, soul_consolidate,
-- decisions_summarise, collective_consolidate), the watch completion
-- summariser, and the semantic judge.
--
-- The FK to llm_conversation_messages stays: NULL bypasses FK checks, so rows
-- that DO have a real message keep their ON DELETE CASCADE cleanup.
--
-- Idempotent: DROP NOT NULL on an already-nullable column is a no-op, which
-- matters because dev was hand-patched to this state on 2026-06-11.

ALTER TABLE public.llm_conversation_token_usage
    ALTER COLUMN message_id DROP NOT NULL;
