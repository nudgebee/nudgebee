-- Migration: Create Prompt Versioning System Tables
-- Version: V658
-- Description: Create tables for prompt version management, experiments, and metrics

-- ============================================================================
-- Table: llm_prompt_configuration
-- Purpose: Store account-specific or provider-specific version overrides
-- ============================================================================

CREATE TABLE IF NOT EXISTS llm_prompt_configuration (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Prompt identification
    prompt_name VARCHAR(255) NOT NULL,
    category VARCHAR(50) NOT NULL
        CHECK (category IN ('agents', 'planners', 'tools', 'utilities')),
    provider VARCHAR(50) NOT NULL DEFAULT 'default'
        CHECK (provider IN ('default', 'bedrock', 'azure', 'openai',
                           'googleai', 'anthropic', 'vertexai', 'huggingface', 'sagemaker')),

    -- Version control
    active_version VARCHAR(20) NOT NULL,

    -- Multi-tenancy
    account_id VARCHAR(255),  -- NULL = global default

    -- Control flags
    enabled BOOLEAN DEFAULT TRUE,
    priority INT DEFAULT 0,

    -- Audit
    notes TEXT,
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    updated_by VARCHAR(255),

    -- Unique constraint
    CONSTRAINT unique_llm_prompt_config
        UNIQUE(prompt_name, category, provider, account_id)
);

CREATE INDEX IF NOT EXISTS idx_llm_prompt_config_lookup
    ON llm_prompt_configuration(prompt_name, category, provider, account_id)
    WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_llm_prompt_config_account
    ON llm_prompt_configuration(account_id)
    WHERE account_id IS NOT NULL AND enabled = TRUE;

-- ============================================================================
-- Table: llm_prompt_experiments
-- Purpose: Test new versions on specific accounts with time windows
-- ============================================================================

CREATE TABLE IF NOT EXISTS llm_prompt_experiments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Identification
    name VARCHAR(255) NOT NULL UNIQUE,
    prompt_name VARCHAR(255) NOT NULL,
    category VARCHAR(50) NOT NULL
        CHECK (category IN ('agents', 'planners', 'tools', 'utilities')),

    -- Version testing
    test_version VARCHAR(20) NOT NULL,
    control_version VARCHAR(20) NOT NULL,

    -- Account targeting
    target_accounts TEXT[] NOT NULL,
        CONSTRAINT non_empty_accounts CHECK (cardinality(target_accounts) > 0),

    -- Provider targeting (optional)
    providers TEXT[],

    -- Time window
    start_date TIMESTAMPTZ,
    end_date TIMESTAMPTZ,
        CONSTRAINT valid_dates CHECK (end_date IS NULL OR end_date > start_date),

    -- Control
    enabled BOOLEAN DEFAULT TRUE,

    -- Metadata
    description TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    created_by VARCHAR(255),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    updated_by VARCHAR(255)
);

CREATE INDEX IF NOT EXISTS idx_llm_experiments_active
    ON llm_prompt_experiments(prompt_name, category, enabled, start_date, end_date)
    WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_llm_experiments_accounts
    ON llm_prompt_experiments USING GIN(target_accounts)
    WHERE enabled = TRUE;

-- ============================================================================
-- Table: llm_prompt_usage_metrics
-- Purpose: Track prompt loading performance and experiment effectiveness
-- ============================================================================

CREATE TABLE IF NOT EXISTS llm_prompt_usage_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Prompt identification
    prompt_name VARCHAR(255) NOT NULL,
    category VARCHAR(50) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    version VARCHAR(20) NOT NULL,

    -- Request context
    account_id VARCHAR(255),
    conversation_id VARCHAR(255),
    agent_name VARCHAR(255),

    -- Performance metrics
    load_time_ms INT,
    cache_hit BOOLEAN,
    config_source VARCHAR(20),

    -- Experiment tracking
    experiment_id UUID,
    experiment_name VARCHAR(255),

    -- Error tracking
    error BOOLEAN DEFAULT FALSE,
    error_message TEXT,

    -- Timestamp
    timestamp TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_llm_metrics_prompt_version
    ON llm_prompt_usage_metrics(prompt_name, version, timestamp DESC);

CREATE INDEX IF NOT EXISTS idx_llm_metrics_experiment
    ON llm_prompt_usage_metrics(experiment_id, timestamp DESC)
    WHERE experiment_id IS NOT NULL;

-- ============================================================================
-- Table: llm_prompt_config_audit
-- Purpose: Audit trail for all prompt configuration changes
-- ============================================================================

CREATE TABLE IF NOT EXISTS llm_prompt_config_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Prompt identification
    prompt_name VARCHAR(255) NOT NULL,
    category VARCHAR(50) NOT NULL,
    provider VARCHAR(50),
    account_id VARCHAR(255),

    -- Change details
    action VARCHAR(50) NOT NULL,
    old_version VARCHAR(20),
    new_version VARCHAR(20),
    experiment_id UUID,

    -- Audit metadata
    changed_by VARCHAR(255),
    changed_at TIMESTAMPTZ DEFAULT NOW(),
    reason TEXT,
    metadata JSONB
);

CREATE INDEX IF NOT EXISTS idx_llm_audit_prompt
    ON llm_prompt_config_audit(prompt_name, category, changed_at DESC);

CREATE INDEX IF NOT EXISTS idx_llm_audit_account
    ON llm_prompt_config_audit(account_id, changed_at DESC)
    WHERE account_id IS NOT NULL;
