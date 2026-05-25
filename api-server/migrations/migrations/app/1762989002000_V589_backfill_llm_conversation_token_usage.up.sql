-- Backfill existing token data from llm_conversation_agent
-- Creates one aggregated record per agent execution

INSERT INTO llm_conversation_token_usage (
    conversation_id,
    message_id,
    agent_id,
    agent_name,
    account_id,
    user_id,
    llm_provider,
    llm_model,
    input_tokens,
    output_tokens,
    cached_input_tokens,
    cache_creation_tokens,
    is_cache_hit,
    cache_hit_rate,
    retry_attempt,
    fallback_from_model,
    fallback_chain,
    latency_seconds,
    request_status,
    error_message,
    content_length,
    stop_reason,
    created_at,
    updated_at
)
SELECT
    a.conversation_id,
    a.message_id,
    a.id,                                    -- agent_id
    a.agent_name,
    a.account_id,
    a.user_id,
    COALESCE(a.llm_provider, 'unknown'),     -- Default if NULL
    COALESCE(a.llm_model, 'unknown'),        -- Default if NULL
    COALESCE(a.input_tokens, 0),
    COALESCE(a.output_tokens, 0),
    COALESCE(a.cached_input_tokens, 0),
    -- cache_creation_tokens: Use cached_input_tokens as best approximation
    -- (Historical data doesn't distinguish between cache reads and cache writes)
    COALESCE(a.cached_input_tokens, 0),
    -- Determine is_cache_hit based on cached_input_tokens
    CASE
        WHEN COALESCE(a.cached_input_tokens, 0) > 0 THEN true
        ELSE false
    END,
    -- Calculate cache_hit_rate as percentage (0-100) to match Go code
    CASE
        WHEN COALESCE(a.input_tokens, 0) > 0
        THEN (COALESCE(a.cached_input_tokens, 0)::float8 / COALESCE(a.input_tokens, 0)::float8) * 100
        ELSE NULL
    END,
    0,                                       -- retry_attempt = 0 (backfilled)
    NULL,                                    -- fallback_from_model = NULL (not tracked)
    NULL,                                    -- fallback_chain = NULL (not tracked historically)
    NULL,                                    -- latency_seconds = NULL (not tracked)
    -- Map agent status to request_status
    CASE
        WHEN a.status = 'success' THEN 'success'
        WHEN a.status = 'fail' THEN 'failure'
        ELSE 'success'
    END,
    NULL,                                    -- error_message = NULL (not in old schema)
    NULL,                                    -- content_length = NULL (not tracked)
    NULL,                                    -- stop_reason = NULL (not tracked)
    a.created_at,
    a.updated_at
FROM llm_conversation_agent a
WHERE
    -- Only backfill agents that made LLM calls (have token usage)
    (COALESCE(a.input_tokens, 0) > 0
     OR COALESCE(a.output_tokens, 0) > 0
     OR COALESCE(a.cached_input_tokens, 0) > 0
     OR COALESCE(a.non_cached_input_tokens, 0) > 0)
    -- Ensure we have required fields
    AND a.llm_provider IS NOT NULL
    AND a.llm_model IS NOT NULL;

-- Log backfill statistics
DO $$
DECLARE
    backfilled_count INT;
    total_input_sum BIGINT;
    total_output_sum BIGINT;
BEGIN
    SELECT COUNT(*), SUM(input_tokens), SUM(output_tokens)
    INTO backfilled_count, total_input_sum, total_output_sum
    FROM llm_conversation_token_usage;

    RAISE NOTICE 'Backfill complete: % records created, % input tokens, % output tokens',
        backfilled_count, total_input_sum, total_output_sum;
END $$;
