ALTER TABLE public.llm_conversation_agent_critiques
    DROP COLUMN IF EXISTS audit_metadata,
    DROP COLUMN IF EXISTS token_usage_id;
