CREATE TABLE IF NOT EXISTS llm_conversation_references (
    id UUID PRIMARY KEY,
    account_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    agent_id UUID NOT NULL,
    reference_id TEXT NOT NULL,
    reference_type VARCHAR(50) NOT NULL,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_conversation_refs_unique ON llm_conversation_references (agent_id, reference_id, reference_type, message_id);

CREATE INDEX if not exists idx_conversation_refs_account ON llm_conversation_references (account_id);

CREATE INDEX if not exists  idx_conversation_refs_conversation ON llm_conversation_references (conversation_id);

CREATE INDEX if not exists  idx_conversation_refs_agent ON llm_conversation_references (agent_id);

