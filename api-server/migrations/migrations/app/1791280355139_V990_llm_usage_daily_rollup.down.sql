SET LOCAL lock_timeout = '5s';

DROP TABLE IF EXISTS public.llm_usage_rollup_state;
DROP TABLE IF EXISTS public.llm_usage_daily;
ALTER TABLE public.llm_conversation_token_usage DROP COLUMN IF EXISTS conversation_account_id;
