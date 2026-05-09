-- Create dedicated budget configuration table
CREATE TABLE IF NOT EXISTS llm_budget_config (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type         VARCHAR(20) NOT NULL,       -- 'tenant' or 'account'
    entity_id           UUID NOT NULL,
    module              VARCHAR(50) NOT NULL,       -- 'investigation' or 'user_investigation'

    -- Super admin: bypass all checks for this entity+module
    budget_disabled     BOOLEAN NOT NULL DEFAULT false,
    disabled_by         VARCHAR(255),
    disabled_at         TIMESTAMPTZ,

    -- Monthly limits (NULL = use system default)
    monthly_cost_limit      NUMERIC(12,2),
    monthly_cost_enabled    BOOLEAN NOT NULL DEFAULT true,
    monthly_count_limit     INTEGER,
    monthly_count_enabled   BOOLEAN NOT NULL DEFAULT false,

    -- Daily limits (NULL = use system default)
    daily_cost_limit        NUMERIC(12,2),
    daily_cost_enabled      BOOLEAN NOT NULL DEFAULT false,
    daily_count_limit       INTEGER,
    daily_count_enabled     BOOLEAN NOT NULL DEFAULT false,

    -- Audit
    updated_by      VARCHAR(255),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    created_at      TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(entity_type, entity_id, module)
);

CREATE INDEX idx_llm_budget_config_entity ON llm_budget_config(entity_type, entity_id);

-- Migrate existing tenant budget configs from tenant_attrs + feature_flag
-- feature_flag schema: feature_id (name), status ('enabled'/'disabled'), tenant_id
INSERT INTO llm_budget_config (entity_type, entity_id, module,
    monthly_cost_limit, monthly_cost_enabled,
    monthly_count_limit, monthly_count_enabled,
    budget_disabled, updated_at)
SELECT
    'tenant',
    ta_limit.tenant_id,
    CASE
        WHEN ta_limit.name = 'llm_budget_limit_investigation' THEN 'investigation'
        WHEN ta_limit.name = 'llm_budget_limit_user_investigation' THEN 'user_investigation'
    END AS module,
    ta_limit.value::NUMERIC(12,2),
    -- cost_enabled always true for migrated rows; budget_disabled carries the bypass state separately
    true,
    -- monthly_count_limit
    (SELECT ta_count.value::INTEGER FROM tenant_attrs ta_count
     WHERE ta_count.tenant_id = ta_limit.tenant_id
     AND ta_count.name = REPLACE(ta_limit.name, 'llm_budget_limit_', 'llm_count_limit_')),
    -- monthly_count_enabled
    COALESCE((SELECT ta_ce.value::BOOLEAN FROM tenant_attrs ta_ce
     WHERE ta_ce.tenant_id = ta_limit.tenant_id
     AND ta_ce.name = REPLACE(ta_limit.name, 'llm_budget_limit_', 'llm_count_limit_enabled_')), false),
    -- budget_disabled
    COALESCE((
        SELECT (ff.status = 'enabled') FROM feature_flag ff
        WHERE ff.tenant_id = ta_limit.tenant_id
        AND ff.feature_id = UPPER('LLM_BUDGET_DISABLED_' ||
            CASE WHEN ta_limit.name = 'llm_budget_limit_investigation' THEN 'INVESTIGATION'
                 ELSE 'USER_INVESTIGATION' END)
    ), false),
    NOW()
FROM tenant_attrs ta_limit
WHERE ta_limit.name IN ('llm_budget_limit_investigation', 'llm_budget_limit_user_investigation')
ON CONFLICT (entity_type, entity_id, module) DO NOTHING;

-- Migrate existing account budget configs from cloud_account_attrs
INSERT INTO llm_budget_config (entity_type, entity_id, module,
    monthly_cost_limit, monthly_cost_enabled,
    budget_disabled, updated_at)
SELECT
    'account',
    ca.cloud_account_id,
    CASE
        WHEN ca.name = 'llm_budget_limit_investigation' THEN 'investigation'
        WHEN ca.name = 'llm_budget_limit_user_investigation' THEN 'user_investigation'
    END AS module,
    ca.value::NUMERIC(12,2),
    -- cost_enabled = NOT disabled
    NOT COALESCE((
        SELECT ca2.value::BOOLEAN FROM cloud_account_attrs ca2
        WHERE ca2.cloud_account_id = ca.cloud_account_id
        AND ca2.name = REPLACE(ca.name, 'llm_budget_limit_', 'llm_budget_disabled_')
    ), false),
    -- budget_disabled
    COALESCE((
        SELECT ca2.value::BOOLEAN FROM cloud_account_attrs ca2
        WHERE ca2.cloud_account_id = ca.cloud_account_id
        AND ca2.name = REPLACE(ca.name, 'llm_budget_limit_', 'llm_budget_disabled_')
    ), false),
    NOW()
FROM cloud_account_attrs ca
WHERE ca.name IN ('llm_budget_limit_investigation', 'llm_budget_limit_user_investigation')
ON CONFLICT (entity_type, entity_id, module) DO NOTHING;
