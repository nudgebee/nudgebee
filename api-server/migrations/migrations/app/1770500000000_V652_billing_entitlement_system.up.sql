-- V650: Billing Entitlement System
-- Creates billing_* tables for feature-based entitlement and usage metering

-- 1. Insert new features into existing feature table
INSERT INTO feature (value, description) VALUES
    ('TROUBLESHOOT', 'AI-powered incident troubleshooting and investigation'),
    ('OPTIMIZE', 'AI-powered cost optimization and recommendations')
ON CONFLICT (value) DO NOTHING;

-- 2. billing_feature_mapping - Maps features/dimensions to AWS metering
CREATE TABLE IF NOT EXISTS billing_feature_mapping (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_id text NOT NULL REFERENCES feature(value),
    dimension text UNIQUE NOT NULL,
    aws_metered_dimension text,
    overage_rate decimal(10,2),
    included_limit_default int,
    description text,
    created_at timestamptz DEFAULT now()
);

INSERT INTO billing_feature_mapping (feature_id, dimension, aws_metered_dimension, overage_rate, included_limit_default, description) VALUES
    ('TROUBLESHOOT', 'incidents', 'ai_troubleshoot', 15.00, 100, 'AI incident investigations per month'),
    ('OPTIMIZE', 'cost_optimization', NULL, NULL, NULL, 'Cost optimization tier (honor system)'),
    ('WORKFLOWS', 'workflow_executions', 'ai_workflow', 0.50, 6000, 'Workflow executions per month'),
    ('WORKFLOWS', 'ai_workflow_steps', 'ai_workflow_function_call', 1.00, 500, 'AI-powered workflow steps per month')
ON CONFLICT (dimension) DO NOTHING;

-- 3. billing_plans - Plan definitions
CREATE TABLE IF NOT EXISTS billing_plans (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    display_name text NOT NULL,
    plan_type text NOT NULL,
    product_code text,
    base_price_monthly decimal(10,2),
    is_active boolean DEFAULT true,
    cost_tier text,
    max_annual_spend int,
    created_at timestamptz DEFAULT now(),
    updated_at timestamptz DEFAULT now(),
    UNIQUE (name, plan_type)
);

-- 4. billing_plan_features - Links plans to features (access control)
CREATE TABLE IF NOT EXISTS billing_plan_features (
    plan_id uuid NOT NULL REFERENCES billing_plans(id) ON DELETE CASCADE,
    feature_id text NOT NULL REFERENCES feature(value),
    created_at timestamptz DEFAULT now(),
    PRIMARY KEY (plan_id, feature_id)
);

-- 5. billing_plan_dimension_limits - Plan-specific dimension limits
CREATE TABLE IF NOT EXISTS billing_plan_dimension_limits (
    plan_id uuid NOT NULL REFERENCES billing_plans(id) ON DELETE CASCADE,
    dimension text NOT NULL,
    included_limit int NOT NULL,
    created_at timestamptz DEFAULT now(),
    PRIMARY KEY (plan_id, dimension)
);

-- 6. billing_subscriptions - Tenant's active plan
CREATE TABLE IF NOT EXISTS billing_subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL UNIQUE REFERENCES tenant(id),
    plan_id uuid NOT NULL REFERENCES billing_plans(id),
    status text DEFAULT 'active',
    subscription_start timestamptz DEFAULT now(),
    subscription_end timestamptz,
    entitlement_overrides jsonb,
    marketplace_customer_id uuid,
    billing_cycle_start timestamptz,
    is_private_contract boolean DEFAULT false,
    created_at timestamptz DEFAULT now(),
    updated_at timestamptz DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_billing_subscriptions_tenant ON billing_subscriptions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_billing_subscriptions_plan ON billing_subscriptions(plan_id);

-- 7. billing_usage - Aggregated usage per dimension per billing period
CREATE TABLE IF NOT EXISTS billing_usage (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant(id),
    billing_period date NOT NULL,
    dimension text NOT NULL,
    usage_count integer DEFAULT 0,
    included_limit integer NOT NULL,
    overage_count integer DEFAULT 0,
    last_reported_overage integer DEFAULT 0,
    created_at timestamptz DEFAULT now(),
    updated_at timestamptz DEFAULT now(),
    UNIQUE (tenant_id, billing_period, dimension)
);

CREATE INDEX IF NOT EXISTS idx_billing_usage_tenant_period ON billing_usage(tenant_id, billing_period);
CREATE INDEX IF NOT EXISTS idx_billing_usage_dimension ON billing_usage(dimension);

-- 8. billing_usage_events - Detailed event audit trail
CREATE TABLE IF NOT EXISTS billing_usage_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant(id),
    dimension text NOT NULL,
    reference_id text NOT NULL,
    reference_type text,
    session_id text,
    is_billable boolean DEFAULT true,
    created_at timestamptz DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_billing_usage_events_tenant ON billing_usage_events(tenant_id);
CREATE INDEX IF NOT EXISTS idx_billing_usage_events_session ON billing_usage_events(tenant_id, session_id);
CREATE INDEX IF NOT EXISTS idx_billing_usage_events_reference ON billing_usage_events(reference_id);
CREATE INDEX IF NOT EXISTS idx_billing_usage_events_dimension ON billing_usage_events(dimension, created_at);
