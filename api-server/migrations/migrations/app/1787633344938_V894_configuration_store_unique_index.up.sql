-- configuration_store (V598) has no unique constraint, so callers can't
-- upsert with ON CONFLICT. Scoped by is_active, not tenant_id IS NOT NULL,
-- so a future soft-delete (is_active=false + new row) never collides.
-- account_id IS NULL keeps this index tenant-scoped only, so it can't block
-- a future account-scoped config type from using the same (tenant, type, key)
-- under a different account_id.
CREATE UNIQUE INDEX IF NOT EXISTS configuration_store_tenant_config_key
    ON configuration_store (tenant_id, config_type, key) WHERE is_active AND account_id IS NULL;
