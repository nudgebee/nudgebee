CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE if not exists llm_conversation_memory (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      UUID NOT NULL,
    conversation_id UUID NULL,
    message_id      UUID NULL,
    memory_type     VARCHAR(50) NOT NULL,
    content         TEXT NOT NULL,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_llm_conv_memory_account_id ON llm_conversation_memory(account_id);
CREATE INDEX IF NOT EXISTS idx_llm_conv_memory_type ON llm_conversation_memory(memory_type);
CREATE INDEX IF NOT EXISTS idx_llm_conv_memory_content_trgm ON llm_conversation_memory USING gin (content gin_trgm_ops);
