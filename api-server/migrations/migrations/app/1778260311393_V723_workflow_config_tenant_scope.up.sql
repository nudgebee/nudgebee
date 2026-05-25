-- Allow tenant-level configs/secrets in addition to account-level.
-- account_id NULL ⇒ tenant-scoped (visible to all accounts in the tenant).
-- account_id set  ⇒ account-scoped (overrides tenant-level row with same key at runtime).

ALTER TABLE configs ALTER COLUMN account_id DROP NOT NULL;

ALTER TABLE configs DROP CONSTRAINT IF EXISTS unique_account_key;

DROP INDEX IF EXISTS idx_configs_account_id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_configs_account_key
    ON configs (account_id, key) WHERE account_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_configs_tenant_key_global
    ON configs (tenant_id, key) WHERE account_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_configs_tenant_id ON configs (tenant_id);
CREATE INDEX IF NOT EXISTS idx_configs_account_id ON configs (account_id) WHERE account_id IS NOT NULL;
