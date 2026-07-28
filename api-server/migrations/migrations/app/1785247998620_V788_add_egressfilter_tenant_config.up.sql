-- Per-tenant override table for the egressfilter package. One row per
-- tenant; absence of a row means env-level defaults apply (fail-open
-- behaviour). The llm-server wrapper consults this row on every LLM call
-- via an in-memory cache (5-min TTL), so the read path stays sub-
-- microsecond and the table itself stays small even at scale.
--
-- Composition rule (enforced by the wrapper, not the DB): tenant config
-- is ADDITIVE on top of env-level baselines. The allowlist extends the
-- env allowlist; disabled_rules removes individual rules from the env-
-- loaded corpus FOR THIS TENANT ONLY; custom_rules adds tenant-specific
-- patterns to that tenant's effective rule set. A tenant cannot weaken
-- the env baseline by removing entries.

CREATE TABLE IF NOT EXISTS public.llm_egressfilter_tenant_config (
    tenant_id       UUID         PRIMARY KEY,
    mode            TEXT         NOT NULL DEFAULT 'detect'
                                 CHECK (mode IN ('detect', 'enforce', 'redact', 'disabled')),
    enabled         BOOLEAN      NOT NULL DEFAULT true,
    -- ["value1", "value2", ...]
    allowlist       JSONB        NOT NULL DEFAULT '[]'::jsonb,
    -- [{"id":"...","regex":"...","enabled":true}, ...]
    -- Column reserved; no admin-API surface in the initial release.
    custom_rules    JSONB        NOT NULL DEFAULT '[]'::jsonb,
    -- ["aws-access-key-id", "private-key-header", ...]
    disabled_rules  JSONB        NOT NULL DEFAULT '[]'::jsonb,
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE public.llm_egressfilter_tenant_config IS
    'Per-tenant overrides for the llm-server egressfilter. Cached in-memory (~5min TTL) on the wrapper read path; admin API mutates this table and invalidates the local cache. Composition rule: tenant config is ADDITIVE on top of env baseline; cannot weaken it.';
