-- Rollback Migration V621: Revert Global Context from Account-Level back to Tenant-Level
-- This rollback removes account_id column and restores the tenant-level unique constraint.

-- Drop the account_id index
DROP INDEX IF EXISTS idx_gc_account_id;

-- Drop the account-level constraint
ALTER TABLE llm_global_contexts
DROP CONSTRAINT IF EXISTS gc_one_per_account;

-- Drop the foreign key constraint to cloud_accounts
ALTER TABLE llm_global_contexts
DROP CONSTRAINT IF EXISTS gc_account_id_fkey;

-- Drop the account_id column
ALTER TABLE llm_global_contexts
DROP COLUMN IF EXISTS account_id;

-- Restore the tenant-level constraint (one GC per tenant)
ALTER TABLE llm_global_contexts
ADD CONSTRAINT gc_one_per_tenant UNIQUE (tenant_id);
