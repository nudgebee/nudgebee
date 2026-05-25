-- Persist Gemini 2.5+/3 hidden chain-of-thought token count
-- (usage_metadata.thoughts_token_count). This is what makes TTFT large for
-- "thinking" models — the model generates these tokens before the visible
-- response begins streaming. Without this column we cannot tell whether a
-- 76s TTFT was 60K thinking tokens at 800 tok/s or a server-side stall.
ALTER TABLE public.llm_conversation_token_usage
ADD COLUMN thinking_tokens int4 NULL;

COMMENT ON COLUMN public.llm_conversation_token_usage.thinking_tokens IS
'Number of hidden chain-of-thought tokens reported by Gemini 2.5+ thinking models (usage.ThoughtsTokenCount). NULL when 0 / unavailable / non-thinking model. Gemini bills these at the output rate.';
