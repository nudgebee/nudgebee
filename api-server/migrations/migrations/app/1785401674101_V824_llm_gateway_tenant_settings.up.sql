-- NB AI Gateway: per-tenant data-governance settings. The gateway reads this on the
-- hot path (hot-reloaded like the routing rules), so a write becomes live on the next
-- store reload. Changes are audited into the shared `audit` table (not here).
-- Idempotent so a re-apply / out-of-band dev apply is a safe no-op.

-- One row per tenant. capture_body opts a tenant INTO full request/response body
-- logging, but only WITHIN the platform ceiling (env gateway_capture_body) —
-- effective capture = ceiling AND this flag, default off (explicit opt-in).
-- egress_filter_mode overrides the env default DLP mode; NULL = inherit the env
-- default, an explicit value (off/detect/enforce/redact) overrides it.
CREATE TABLE IF NOT EXISTS llm_gateway_tenant_settings (
    tenant_id          text        PRIMARY KEY,
    capture_body       boolean     NOT NULL DEFAULT false,
    egress_filter_mode text,                                  -- NULL = inherit env default
    updated_at         timestamptz NOT NULL DEFAULT now(),
    updated_by         text        NOT NULL DEFAULT ''
);
