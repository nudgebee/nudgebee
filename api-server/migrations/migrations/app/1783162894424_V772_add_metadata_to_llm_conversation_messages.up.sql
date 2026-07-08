-- Add a generic metadata JSONB column to llm_conversation_messages so per-
-- message subsystems can attach structured artifacts under their own top-
-- level keys without each subsystem needing its own table or column.
--
-- First consumer: the outbound egressfilter writes under "egressfilter" with
-- the audit id, rule ids, mode, and hit counts for any egressfilter event
-- that fired during the message's LLM calls. UI reads this and renders a
-- per-message badge / banner / icon as it sees fit.
--
-- The column is nullable — most messages will not carry metadata and we do
-- not want to bloat storage with empty objects.
ALTER TABLE public.llm_conversation_messages
    ADD COLUMN IF NOT EXISTS metadata jsonb NULL;
