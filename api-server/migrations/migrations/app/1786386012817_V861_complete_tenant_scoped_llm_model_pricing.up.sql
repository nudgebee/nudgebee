-- Complete the tenant-scoped `llm_model_pricing` schema.
--
-- The feature code (llm-server UpsertModelPricing/DeleteModelPricing/ListModelPricing + the
-- "Model Pricing" admin UI) shipped in commit 6c4dcd5854 (nb-35189), but its migration
-- `V844_tenant_scoped_llm_model_pricing` was committed as an EMPTY stub. As a result the
-- tenant-scoping columns, FK, and partial unique indexes were applied to already-running
-- environments out-of-band (they exist on dev) but are absent on a clean build — where the
-- tenant upsert/delete SQL (`ON CONFLICT (model_name, provider_name, tenant_id)`,
-- `pricing_updated_at`, ...) would fail at runtime. This migration captures that schema so
-- every environment converges.
--
-- Every statement is idempotent (IF NOT EXISTS / guarded), so this is a safe no-op on an env
-- that was already hand-patched, and does the real work on a clean/prod DB.

ALTER TABLE llm_model_pricing
    ADD COLUMN IF NOT EXISTS tenant_id          uuid,
    ADD COLUMN IF NOT EXISTS pricing_updated_at timestamptz,
    ADD COLUMN IF NOT EXISTS pricing_updated_by uuid;

-- tenant_id -> tenant(id); a deleted tenant takes its custom price rows with it. ADD CONSTRAINT
-- has no IF NOT EXISTS, so guard on the catalog.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'llm_model_pricing_tenant_id_fkey'
    ) THEN
        ALTER TABLE llm_model_pricing
            ADD CONSTRAINT llm_model_pricing_tenant_id_fkey
            FOREIGN KEY (tenant_id) REFERENCES tenant(id) ON DELETE CASCADE;
    END IF;
END $$;

-- Replace the original global UNIQUE(model_name, provider_name) (from V566) with two PARTIAL
-- uniques: a built-in row (tenant_id IS NULL) stays unique per (model_name, provider_name),
-- while a tenant may add its own override row keyed by (model_name, provider_name, tenant_id).
-- Leaving the global constraint in place would stop two tenants from ever pricing the same
-- model. The table is small, so plain (non-CONCURRENT) index builds are fine here.
ALTER TABLE llm_model_pricing
    DROP CONSTRAINT IF EXISTS llm_model_pricing_model_name_provider_name_key;

CREATE UNIQUE INDEX IF NOT EXISTS llm_model_pricing_builtin_key
    ON llm_model_pricing (model_name, provider_name) WHERE tenant_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS llm_model_pricing_tenant_key
    ON llm_model_pricing (model_name, provider_name, tenant_id) WHERE tenant_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS llm_model_pricing_tenant_lookup
    ON llm_model_pricing (tenant_id) WHERE tenant_id IS NOT NULL;
