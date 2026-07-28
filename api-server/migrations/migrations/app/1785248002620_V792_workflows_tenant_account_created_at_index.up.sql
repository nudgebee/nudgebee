-- Speeds up workflow_list: the query always filters on (tenant_id, account_id)
-- and always orders by created_at DESC, but no existing index covers the sort
-- column, so Postgres falls back to a sort step once account_id narrows the
-- scan. This index lets both the filter and the ORDER BY use the same index.
CREATE INDEX IF NOT EXISTS idx_workflows_tenant_account_created_at ON workflows (tenant_id, account_id, created_at DESC);
