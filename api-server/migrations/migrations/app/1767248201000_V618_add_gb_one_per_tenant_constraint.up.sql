-- V618: Add unique constraint to enforce one global context per tenant
-- This migration enforces that each tenant can have only ONE global context

-- First, drop the existing unique constraint that allows multiple GBs per tenant with different names
ALTER TABLE llm_global_contexts DROP CONSTRAINT IF EXISTS gc_name_tenant_unique;

-- Add new unique constraint on tenant_id to enforce one GB per tenant
ALTER TABLE llm_global_contexts ADD CONSTRAINT gc_one_per_tenant UNIQUE (tenant_id);

-- Add index on tenant_id if it doesn't exist (should already exist from V617)
-- CREATE INDEX IF NOT EXISTS idx_gc_tenant_id ON llm_global_contexts(tenant_id);

COMMENT ON CONSTRAINT gc_one_per_tenant ON llm_global_contexts IS 'Enforce one global context per tenant';
