-- Extend the per-tenant egressfilter override row (V773) with per-tenant
-- controls for the PII sibling detector that landed in PR #31514
-- (ee/scrubbing wrapper around ml-k8s-server /scrub).
--
-- All four new columns are NULLABLE (except pii_disabled_categories which
-- defaults to '{}') so the wrapper can distinguish "tenant hasn't touched
-- this" (NULL → inherit env) from "tenant explicitly set false" (explicit
-- override). This is a departure from the existing `enabled` column
-- (V773, secrets side), where a NOT NULL default TRUE works because
-- secrets is enabled at env level by default; adding a tenant row means
-- the admin has actively configured the row.
--
-- PII inverts that: env default is FALSE (introduces an ml-k8s-server
-- runtime dependency), so a NOT NULL PII default would either force
-- every existing tenant row into a PII posture they never asked for
-- (default TRUE) or require explicit opt-in from every tenant even when
-- env is on (default FALSE, ignoring env). Nullable + tri-state avoids
-- both footguns.
--
-- Semantics enforced by the wrapper (llm/llm-server/ee/scrubbing,
-- llm/llm-server/security/egressfilter/tenant_config.go):
--   - Wrapper installs only when env LLM_SERVER_EGRESSFILTER_PII_ENABLED
--     is true (the ml-k8s dependency has to be provisioned). Env=off →
--     tenant config has no effect (there is nothing to scrub through).
--   - When the wrapper IS installed, per-request Resolve(ctx) reads the
--     tenant row and applies overrides:
--       * pii_enabled=FALSE           → skip scrubbing for this tenant
--       * pii_mode='enforce'          → fail-closed on scrubber outage
--       * pii_ner_enabled=FALSE       → strip NER from the /scrub request
--       * pii_disabled_categories=[..]→ drop matching tokens from the
--                                       mapping before rewrite
--   - NULL / empty fields inherit the env default for that field.

ALTER TABLE public.llm_egressfilter_tenant_config
    ADD COLUMN pii_enabled              BOOLEAN,
    ADD COLUMN pii_mode                 TEXT
                                        CHECK (pii_mode IS NULL OR pii_mode IN ('detect', 'enforce')),
    ADD COLUMN pii_ner_enabled          BOOLEAN,
    -- Category names come from the ml-k8s /scrub token namespace
    -- (EMAIL, PERSON, PHONE, LOCATION). Stored as uppercase text[] so
    -- the wrapper can do set membership in Go without JSON parsing.
    -- Default '{}' (empty array, NOT NULL) so the wrapper always receives
    -- a well-shaped value; empty = no category is disabled.
    ADD COLUMN pii_disabled_categories  TEXT[] NOT NULL DEFAULT '{}';

COMMENT ON COLUMN public.llm_egressfilter_tenant_config.pii_enabled IS
    'Tri-state per-tenant override for the PII scrubber. NULL = inherit env LLM_SERVER_EGRESSFILTER_PII_ENABLED; TRUE/FALSE = explicit. Note: env=off is a hard gate (ml-k8s dependency), so tenant TRUE with env FALSE is a no-op.';
COMMENT ON COLUMN public.llm_egressfilter_tenant_config.pii_mode IS
    'Tri-state per-tenant PII outage policy. NULL = inherit env LLM_SERVER_EGRESSFILTER_PII_MODE; ''detect'' = fail-open on scrubber outage; ''enforce'' = fail-closed (HIPAA/GDPR).';
COMMENT ON COLUMN public.llm_egressfilter_tenant_config.pii_ner_enabled IS
    'Tri-state per-tenant NER opt-in. NULL = inherit env; TRUE/FALSE = explicit. NER is fuzzy on ops text so tenants vary in appetite.';
COMMENT ON COLUMN public.llm_egressfilter_tenant_config.pii_disabled_categories IS
    'Categories whose tokens the wrapper drops from the /scrub mapping before rewriting messages. Valid values match the ml-k8s /scrub token namespace (EMAIL, PERSON, PHONE, LOCATION). Empty = scrub all categories.';
