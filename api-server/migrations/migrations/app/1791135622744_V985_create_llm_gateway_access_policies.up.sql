-- AI Gateway access policies (#38966): which providers, models and endpoint families a gateway
-- key may reach, one row per allowed value. Gateway-owned and scoped like
-- llm_gateway_rate_limits ('' = any), so per-user (#38963) and tenant-default rows fit the same
-- shape. Only key-scope rows (user_id and api_key_id both set) are written and enforced today;
-- the gateway ignores user- and tenant-scope rows until #38963 adds their evaluator.
--
-- A key-scope row is matched by api_key_id and user_id alone. A key's user_auths.tenant_id
-- follows its owner's current tenant (UpdateUserAccessed rewrites it on a tenant switch), so
-- matching the tenant too would drop the restriction; tenant_id records where it was written.
--
-- No rows for a dimension at a scope leaves that dimension unrestricted, which is every key
-- that exists today. A new, empty table: no lock on any existing table.
CREATE TABLE IF NOT EXISTS public.llm_gateway_access_policies (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   text        NOT NULL,
    user_id     text        NOT NULL DEFAULT '',  -- '' = every user of the tenant
    api_key_id  text        NOT NULL DEFAULT '',  -- '' = every key of the user
    dimension   text        NOT NULL CHECK (dimension IN ('provider', 'model', 'endpoint')),
    value       text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- A key row must name its user, or it would read as a tenant-wide row.
    CONSTRAINT llm_gateway_access_policies_scope CHECK (user_id <> '' OR api_key_id = ''),
    -- Key first: its index serves the gateway's per-request lookup by (api_key_id, user_id).
    CONSTRAINT llm_gateway_access_policies_entry UNIQUE (api_key_id, user_id, tenant_id, dimension, value)
);
