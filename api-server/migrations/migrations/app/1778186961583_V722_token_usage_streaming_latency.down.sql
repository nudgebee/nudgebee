ALTER TABLE public.llm_conversation_token_usage
DROP COLUMN IF EXISTS ttft_ms,
DROP COLUMN IF EXISTS itl_ms_avg,
DROP COLUMN IF EXISTS tokens_per_second,
DROP COLUMN IF EXISTS was_streaming;
