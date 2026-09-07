-- Nullable additions only: no backfill or table rewrite. ALTER requires a brief
-- metadata lock. Existing critique producers need neither field.
ALTER TABLE public.llm_conversation_agent_critiques
    ADD COLUMN IF NOT EXISTS token_usage_id uuid,
    ADD COLUMN IF NOT EXISTS audit_metadata jsonb;

COMMENT ON COLUMN public.llm_conversation_agent_critiques.token_usage_id IS
'Exact expected llm_conversation_token_usage.id; usage is asynchronous, so absent rows mean unavailable cost. No FK.';
COMMENT ON COLUMN public.llm_conversation_agent_critiques.audit_metadata IS
'Versioned evidence, prompt, baseline, capture status and completion time for claim_shadow critiques. NULL for legacy critiques.';
