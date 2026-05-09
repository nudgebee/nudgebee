-- Speed up llm_conversation_list_v2 initial load.
-- Existing index (account_id, created_at DESC) cannot serve ORDER BY updated_at DESC,
-- forcing a full sort of the account's conversations on every sidebar open.
-- This index lets the planner walk in updated_at DESC order, eliminating the sort
-- and supporting the polling case (updated_at > $last) as a range scan.
CREATE INDEX IF NOT EXISTS idx_llm_conversations_account_updated_at
  ON public.llm_conversations (account_id, updated_at DESC);
