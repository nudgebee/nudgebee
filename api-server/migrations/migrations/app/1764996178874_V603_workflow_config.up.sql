CREATE TABLE IF NOT EXISTS configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key CITEXT NOT NULL,
    value TEXT NOT NULL,
    type VARCHAR(50) NOT NULL CHECK (type IN ('config', 'secret')),
    labels JSONB,
    metadata JSONB,
    tenant_id UUID NOT NULL,
    account_id UUID NOT NULL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now(),
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now(),
    created_by UUID,
    updated_by UUID,
    CONSTRAINT unique_account_key UNIQUE (account_id, key)
);

CREATE INDEX IF NOT EXISTS idx_configs_account_id ON configs (account_id);