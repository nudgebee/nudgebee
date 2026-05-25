-- Rollback: Remove KG queue deduplication index
DROP INDEX IF EXISTS idx_kg_filters_tenant_last_sync;
