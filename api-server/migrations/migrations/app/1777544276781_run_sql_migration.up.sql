CREATE INDEX IF NOT EXISTS idx_spends_resource_account
ON spends (cloud_resource_id, cloud_account) INCLUDE (amount);
