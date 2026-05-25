-- Reverse tenant-scope support. Any rows with account_id IS NULL must be removed first
-- because the original NOT NULL constraint cannot accept them.

DELETE FROM configs WHERE account_id IS NULL;

DROP INDEX IF EXISTS uq_configs_tenant_key_global;
DROP INDEX IF EXISTS uq_configs_account_key;
DROP INDEX IF EXISTS idx_configs_tenant_id;
DROP INDEX IF EXISTS idx_configs_account_id;

ALTER TABLE configs ALTER COLUMN account_id SET NOT NULL;

ALTER TABLE configs ADD CONSTRAINT unique_account_key UNIQUE (account_id, key);

CREATE INDEX IF NOT EXISTS idx_configs_account_id ON configs (account_id);
