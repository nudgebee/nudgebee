ALTER TABLE public.llm_egressfilter_tenant_config
    DROP COLUMN IF EXISTS pii_enabled,
    DROP COLUMN IF EXISTS pii_mode,
    DROP COLUMN IF EXISTS pii_ner_enabled,
    DROP COLUMN IF EXISTS pii_disabled_categories;
