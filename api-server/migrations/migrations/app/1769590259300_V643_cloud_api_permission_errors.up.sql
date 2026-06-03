CREATE TABLE IF NOT EXISTS cloud_api_permission_errors (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    cloud_account_id UUID NOT NULL,
    account_number VARCHAR(64) NOT NULL,
    cloud_provider VARCHAR(16) NOT NULL,
    service_name VARCHAR(128) NOT NULL,
    api_operation VARCHAR(256) NOT NULL,
    wrapper_method VARCHAR(64) NOT NULL DEFAULT '',
    error_code VARCHAR(128) NOT NULL,
    error_message TEXT,
    region VARCHAR(64) NOT NULL DEFAULT '',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    occurrence_count INT NOT NULL DEFAULT 1,
    is_resolved BOOLEAN NOT NULL DEFAULT FALSE,
    resolved_at TIMESTAMPTZ,
    UNIQUE(tenant_id, cloud_account_id, service_name, api_operation, error_code, region)
);

CREATE INDEX IF NOT EXISTS idx_perm_errors_account ON cloud_api_permission_errors(cloud_account_id);
CREATE INDEX IF NOT EXISTS idx_perm_errors_tenant ON cloud_api_permission_errors(tenant_id);
CREATE INDEX IF NOT EXISTS idx_perm_errors_unresolved ON cloud_api_permission_errors(tenant_id, is_resolved) WHERE is_resolved = FALSE;
