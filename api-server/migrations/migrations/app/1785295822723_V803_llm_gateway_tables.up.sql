-- NB AI Gateway (llm/gateway) tables. Re-homed from llm/gateway/metering/migrations
-- so the central migrate job owns them. All statements are idempotent so this is a
-- safe no-op where the tables were already applied out-of-band (e.g. dev).
-- No CONCURRENTLY (golang-migrate wraps each migration in a transaction).

-- llm_gateway_usage: one row per proxied request (all traffic). provider/model hold
-- what actually RAN (resolved); requested_* + routing_* capture what the client asked
-- for and why routing changed it. Cost is computed on read via the pricing catalog.
CREATE TABLE IF NOT EXISTS llm_gateway_usage (
    id                 uuid        PRIMARY KEY,
    created_at         timestamptz NOT NULL DEFAULT now(),

    -- identity (populated by auth; empty when unauthenticated)
    tenant_id          text        NOT NULL DEFAULT '',
    account_id         text        NOT NULL DEFAULT '',
    user_id            text        NOT NULL DEFAULT '',
    token_id           text        NOT NULL DEFAULT '',

    -- correlation + structure-only attributes (client-supplied; content excluded)
    session_id         text        NOT NULL DEFAULT '',
    attributes         text        NOT NULL DEFAULT '{}',  -- JSON: {"actual":…,"derived":…}

    -- request
    provider           text        NOT NULL,
    model              text        NOT NULL DEFAULT '',
    method             text        NOT NULL,
    path               text        NOT NULL,
    streaming          boolean     NOT NULL DEFAULT false,

    -- response
    status_code        int         NOT NULL,
    latency_ms         bigint      NOT NULL DEFAULT 0,
    request_id         text        NOT NULL DEFAULT '',

    -- usage (raw; cost computed on read via the pricing catalog)
    input_tokens       int         NOT NULL DEFAULT 0,
    output_tokens      int         NOT NULL DEFAULT 0,
    total_tokens       int         NOT NULL DEFAULT 0,
    cache_read_tokens  int         NOT NULL DEFAULT 0,
    cache_write_tokens int         NOT NULL DEFAULT 0,
    service_tier       text        NOT NULL DEFAULT '',

    -- routing decision capture (requested vs resolved + why)
    requested_provider text        NOT NULL DEFAULT '',
    requested_model    text        NOT NULL DEFAULT '',
    routing_reason     text        NOT NULL DEFAULT '',
    routing_rule       text        NOT NULL DEFAULT ''
);

-- Protective for any partial state where the table pre-existed without the routing
-- columns (an older dev DB): the CREATE above skips an existing table.
ALTER TABLE llm_gateway_usage ADD COLUMN IF NOT EXISTS requested_provider text NOT NULL DEFAULT '';
ALTER TABLE llm_gateway_usage ADD COLUMN IF NOT EXISTS requested_model    text NOT NULL DEFAULT '';
ALTER TABLE llm_gateway_usage ADD COLUMN IF NOT EXISTS routing_reason     text NOT NULL DEFAULT '';
ALTER TABLE llm_gateway_usage ADD COLUMN IF NOT EXISTS routing_rule       text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_llm_gateway_usage_created_at ON llm_gateway_usage (created_at);
CREATE INDEX IF NOT EXISTS idx_llm_gateway_usage_tenant     ON llm_gateway_usage (tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_llm_gateway_usage_provider   ON llm_gateway_usage (provider, created_at);
CREATE INDEX IF NOT EXISTS idx_llm_gateway_usage_session    ON llm_gateway_usage (session_id, created_at) WHERE session_id <> '';
CREATE INDEX IF NOT EXISTS idx_llm_gateway_usage_routing    ON llm_gateway_usage (routing_reason, created_at) WHERE routing_reason <> 'passthrough';

-- llm_gateway_request_log: full request/response bodies, captured ONLY when body
-- logging is enabled (off by default; data-custody sensitive). Rows carry a TTL
-- (expires_at) and are soft-deleted (deleted_at) by the gateway's cleanup job.
CREATE TABLE IF NOT EXISTS llm_gateway_request_log (
    id            uuid        PRIMARY KEY,
    request_id    uuid        NOT NULL,               -- links to llm_gateway_usage.id
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,               -- created_at + TTL
    deleted_at    timestamptz,                        -- set by the soft-cleanup job

    -- identity + correlation (from auth/session; empty when unauthenticated)
    tenant_id     text        NOT NULL DEFAULT '',
    user_id       text        NOT NULL DEFAULT '',
    session_id    text        NOT NULL DEFAULT '',

    -- request context
    provider      text        NOT NULL,
    model         text        NOT NULL DEFAULT '',
    method        text        NOT NULL,
    path          text        NOT NULL,
    status_code   int         NOT NULL,

    -- bodies (capped at gateway_body_max_bytes; truncation marker appended if over)
    request_body  text        NOT NULL DEFAULT '',
    response_body text        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_llm_gw_reqlog_expiry  ON llm_gateway_request_log (expires_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_llm_gw_reqlog_request ON llm_gateway_request_log (request_id);
CREATE INDEX IF NOT EXISTS idx_llm_gw_reqlog_tenant  ON llm_gateway_request_log (tenant_id, created_at);

-- llm_gateway_routing_rules: per-tenant routing rules (metastore). tenant_id='' =
-- global default. match/target are the JSON shapes in routing/types.go. Config-file
-- rules layer on top of these (DB rules win ties).
CREATE TABLE IF NOT EXISTS llm_gateway_routing_rules (
    id         uuid        PRIMARY KEY,
    tenant_id  text        NOT NULL DEFAULT '',   -- '' = global
    priority   int         NOT NULL DEFAULT 100,  -- lower evaluated first
    enabled    boolean     NOT NULL DEFAULT true,
    match      jsonb       NOT NULL DEFAULT '{}',
    target     jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_llm_gw_routing_tenant ON llm_gateway_routing_rules (tenant_id, priority) WHERE enabled;

-- llm_gateway_rate_limits: per-tenant/user quota config (metastore). Live counters
-- live in Redis; this is just the limits. A scope can have several rows (metric ×
-- period). tenant_id required; user_id='' = tenant-wide.
CREATE TABLE IF NOT EXISTS llm_gateway_rate_limits (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   text        NOT NULL DEFAULT '',
    user_id     text        NOT NULL DEFAULT '',   -- '' = tenant-wide
    metric      text        NOT NULL,              -- requests | tokens | cost
    period      text        NOT NULL,              -- minute | hour | day | month
    limit_value numeric     NOT NULL,              -- cap per period (count / tokens / USD)
    enabled     boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_llm_gw_ratelimits_scope ON llm_gateway_rate_limits (tenant_id, user_id) WHERE enabled;
