-- Reverse V861: remove the tenant-scoping schema from llm_model_pricing. Run only to roll the
-- tenant-pricing feature back — it discards what distinguishes tenant override rows.

DROP INDEX IF EXISTS llm_model_pricing_tenant_lookup;
DROP INDEX IF EXISTS llm_model_pricing_tenant_key;
DROP INDEX IF EXISTS llm_model_pricing_builtin_key;

ALTER TABLE llm_model_pricing
    DROP CONSTRAINT IF EXISTS llm_model_pricing_tenant_id_fkey;

ALTER TABLE llm_model_pricing
    DROP COLUMN IF EXISTS pricing_updated_by,
    DROP COLUMN IF EXISTS pricing_updated_at,
    DROP COLUMN IF EXISTS tenant_id;

-- Restore the original global uniqueness. Best-effort: this fails if tenant overrides had
-- created duplicate (model_name, provider_name) pairs — de-duplicate before rolling back.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'llm_model_pricing_model_name_provider_name_key'
    ) THEN
        ALTER TABLE llm_model_pricing
            ADD CONSTRAINT llm_model_pricing_model_name_provider_name_key UNIQUE (model_name, provider_name);
    END IF;
END $$;
