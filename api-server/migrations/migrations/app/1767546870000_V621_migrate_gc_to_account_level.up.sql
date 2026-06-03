-- Migration V621: Migrate Global Context from Tenant-Level to Account-Level
-- This migration adds account_id column and changes the unique constraint
-- from tenant-level to account-level, allowing one GC per account instead of one per tenant.

-- Add account_id column (nullable initially to allow existing data)
ALTER TABLE llm_global_contexts
ADD COLUMN account_id UUID;

-- Add foreign key constraint to cloud_accounts
ALTER TABLE llm_global_contexts
ADD CONSTRAINT gc_account_id_fkey
FOREIGN KEY (account_id)
REFERENCES public.cloud_accounts(id)
ON DELETE RESTRICT ON UPDATE RESTRICT;

-- Drop old tenant-level constraint (one GC per tenant)
ALTER TABLE llm_global_contexts
DROP CONSTRAINT IF EXISTS gc_one_per_tenant;

-- Add new account-level constraint (one GC per account)
ALTER TABLE llm_global_contexts
ADD CONSTRAINT gc_one_per_account UNIQUE (account_id);

-- Add index for account_id queries to improve performance
CREATE INDEX IF NOT EXISTS idx_gc_account_id
ON llm_global_contexts(account_id);

-- Note: account_id is left nullable for now. Existing tenant-level GCs will have account_id = NULL
-- and will become inaccessible via the new account-scoped API.
-- Users must create new GCs manually per account.
-- To enforce NOT NULL constraint later, run:
-- ALTER TABLE llm_global_contexts ALTER COLUMN account_id SET NOT NULL;
