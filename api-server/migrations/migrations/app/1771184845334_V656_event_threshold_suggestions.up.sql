CREATE TABLE IF NOT EXISTS event_threshold_suggestions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fingerprint TEXT NOT NULL,
    cloud_account_id UUID NOT NULL,
    tenant_id UUID NOT NULL,
    source TEXT NOT NULL,

    -- Alert definition
    alert_name TEXT,
    metric_name TEXT NOT NULL,
    metric_namespace TEXT,
    current_threshold NUMERIC NOT NULL,
    operator TEXT,
    aggregation TEXT,

    -- Suggestion
    suggested_threshold NUMERIC NOT NULL,
    reason TEXT NOT NULL,
    confidence TEXT CHECK (confidence IN ('low', 'medium', 'high')),
    estimated_reduction NUMERIC,

    -- Analysis data (JSONB for flexibility)
    firing_analysis JSONB,
    metric_stats JSONB,

    -- Lifecycle
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(fingerprint, cloud_account_id)
);

CREATE INDEX idx_ets_tenant ON event_threshold_suggestions(tenant_id, cloud_account_id);
CREATE INDEX idx_ets_fingerprint ON event_threshold_suggestions(fingerprint);
