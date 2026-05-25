-- Create llm_conversation_token_usage table for granular per-API-call token tracking
CREATE TABLE public.llm_conversation_token_usage (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    conversation_id uuid NOT NULL,
    message_id uuid NOT NULL,
    agent_id uuid NULL,
    agent_name text NOT NULL,
    account_id uuid NOT NULL,
    user_id uuid NULL,
    llm_provider text NOT NULL,
    llm_model text NOT NULL,

    -- Token metrics (core fields - no redundancy)
    input_tokens int4 DEFAULT 0 NOT NULL,
    output_tokens int4 DEFAULT 0 NOT NULL,
    cached_input_tokens int4 DEFAULT 0 NOT NULL,
    cache_creation_tokens int4 DEFAULT 0 NOT NULL,

    -- Cache metrics (for query convenience)
    is_cache_hit boolean DEFAULT false NOT NULL,
    cache_hit_rate float8 NULL,

    -- Retry and fallback tracking
    retry_attempt int4 DEFAULT 0 NOT NULL,
    fallback_from_model text NULL,
    fallback_chain jsonb NULL,

    -- Performance metrics
    latency_seconds float8 NULL,

    -- Status tracking
    request_status text NOT NULL DEFAULT 'success',
    error_message text NULL,

    -- Additional metadata
    content_length int4 NULL,
    stop_reason text NULL,

    -- Timestamps
    created_at timestamp DEFAULT now() NOT NULL,
    updated_at timestamp DEFAULT now() NOT NULL,

    CONSTRAINT llm_conversation_token_usage_pk PRIMARY KEY (id),
    CONSTRAINT llm_conversation_token_usage_status_check
        CHECK ((request_status = ANY (ARRAY['success'::text, 'failure'::text]))),
    CONSTRAINT llm_conversation_token_usage_conversation_fk
        FOREIGN KEY (conversation_id) REFERENCES public.llm_conversations(id) ON DELETE CASCADE,
    CONSTRAINT llm_conversation_token_usage_message_fk
        FOREIGN KEY (message_id) REFERENCES public.llm_conversation_messages(id) ON DELETE CASCADE,
    CONSTRAINT llm_conversation_token_usage_agent_fk
        FOREIGN KEY (agent_id) REFERENCES public.llm_conversation_agent(id) ON DELETE SET NULL
);

-- Add comment
COMMENT ON TABLE public.llm_conversation_token_usage IS
'Granular per-API-call token usage tracking for LLM requests. Replaces aggregated token columns in llm_conversation_agent table.';
