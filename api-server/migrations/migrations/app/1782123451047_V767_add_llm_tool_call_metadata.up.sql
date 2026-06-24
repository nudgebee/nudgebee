-- Adds a single JSONB column to carry tool-execution metadata that the planner
-- needs at prompt-assembly time but the UI does not render. Today this holds
-- exit_status, execution_duration_ms, stderr, truncated, original_len. New
-- fields (token_usage, cache_hit, workspace_id, ...) land here without a
-- migration.
--
-- Nullable + no default — historical rows stay NULL; the planner formats no
-- footer for rows without metadata, which is the same shape as pre-deploy.
ALTER TABLE public.llm_conversation_tool_calls
    ADD COLUMN IF NOT EXISTS metadata JSONB NULL;
