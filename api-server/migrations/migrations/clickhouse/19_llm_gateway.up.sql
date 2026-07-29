-- NB AI Gateway (llm/gateway) ClickHouse tables, re-homed from
-- llm/gateway/metering/migrations/clickhouse. Applied by the central migrate job
-- when CLICKHOUSE_ENABLED=true. Idempotent (safe no-op where already applied).
-- Requires the clickhouse driver blank-imported in the gateway before the CH sink
-- is usable. MergeTree, partitioned by month, ordered for (tenant,time)/(provider,time).
CREATE TABLE IF NOT EXISTS llm_gateway_usage (
    id                 String,
    created_at         DateTime64(3) DEFAULT now64(3),

    tenant_id          String DEFAULT '',
    account_id         String DEFAULT '',
    user_id            String DEFAULT '',
    token_id           String DEFAULT '',

    session_id         String DEFAULT '',
    attributes         String DEFAULT '{}',  -- JSON: {"actual":…,"derived":…}

    provider           String,
    model              String DEFAULT '',
    method             String,
    path               String,
    streaming          UInt8  DEFAULT 0,

    status_code        Int32,
    latency_ms         Int64  DEFAULT 0,
    request_id         String DEFAULT '',

    input_tokens       Int32  DEFAULT 0,
    output_tokens      Int32  DEFAULT 0,
    total_tokens       Int32  DEFAULT 0,
    cache_read_tokens  Int32  DEFAULT 0,
    cache_write_tokens Int32  DEFAULT 0,
    service_tier       String DEFAULT '',

    -- routing decision capture (requested vs resolved + why)
    requested_provider String DEFAULT '',
    requested_model    String DEFAULT '',
    routing_reason     String DEFAULT '',
    routing_rule       String DEFAULT ''
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(created_at)
ORDER BY (tenant_id, created_at, provider);

-- Protective for an older CH table created before the routing columns existed.
ALTER TABLE llm_gateway_usage ADD COLUMN IF NOT EXISTS requested_provider String DEFAULT '';
ALTER TABLE llm_gateway_usage ADD COLUMN IF NOT EXISTS requested_model    String DEFAULT '';
ALTER TABLE llm_gateway_usage ADD COLUMN IF NOT EXISTS routing_reason     String DEFAULT '';
ALTER TABLE llm_gateway_usage ADD COLUMN IF NOT EXISTS routing_rule       String DEFAULT '';

-- Bodies captured only when body logging is enabled. TTL enforced natively; the
-- gateway's soft cleanup also runs for parity with Postgres.
CREATE TABLE IF NOT EXISTS llm_gateway_request_log (
    id            String,
    request_id    String,
    created_at    DateTime64(3) DEFAULT now64(3),
    expires_at    DateTime64(3),
    deleted_at    Nullable(DateTime64(3)),

    tenant_id     String DEFAULT '',
    user_id       String DEFAULT '',
    session_id    String DEFAULT '',

    provider      String,
    model         String DEFAULT '',
    method        String,
    path          String,
    status_code   Int32,

    request_body  String DEFAULT '',
    response_body String DEFAULT ''
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(created_at)
ORDER BY (tenant_id, created_at)
TTL toDateTime(expires_at) DELETE;
