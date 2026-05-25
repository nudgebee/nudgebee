-- Per-call streaming latency breakdown so dashboards can distinguish slow
-- gateway (high TTFT) from slow generation (high ITL).
ALTER TABLE public.llm_conversation_token_usage
ADD COLUMN ttft_ms          int4   NULL,
ADD COLUMN itl_ms_avg        float4 NULL,
ADD COLUMN tokens_per_second float4 NULL,
ADD COLUMN was_streaming     bool   NULL;

COMMENT ON COLUMN public.llm_conversation_token_usage.ttft_ms IS
'Time-to-first-token in milliseconds (streaming calls only). NULL when the call did not stream or no chunk was observed.';

COMMENT ON COLUMN public.llm_conversation_token_usage.itl_ms_avg IS
'Average inter-token latency in milliseconds, derived as (latency_ms - ttft_ms) / output_tokens. float4 to preserve sub-ms precision on fast models. NULL when output_tokens=0 or generation_ms<=0.';

COMMENT ON COLUMN public.llm_conversation_token_usage.tokens_per_second IS
'Steady-state output throughput: output_tokens / (latency_seconds - ttft_ms/1000).';

COMMENT ON COLUMN public.llm_conversation_token_usage.was_streaming IS
'Whether this LLM call used streaming. NULL on legacy rows; FALSE/TRUE for new rows.';
