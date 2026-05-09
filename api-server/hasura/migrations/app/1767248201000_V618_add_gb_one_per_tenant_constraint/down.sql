-- V618 Rollback: Remove one-per-tenant constraint and restore name uniqueness

-- Remove the one-per-tenant constraint
ALTER TABLE llm_global_contexts DROP CONSTRAINT IF EXISTS gc_one_per_tenant;

-- Restore the original unique constraint on (tenant_id, name)
ALTER TABLE llm_global_contexts ADD CONSTRAINT gc_name_tenant_unique UNIQUE (tenant_id, name);
