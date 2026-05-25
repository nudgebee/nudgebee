-- Track lifecycle of provider-side prompt caches across all scopes (global,
-- tenant, account, conversation). Storage cost is per-cache-lifetime, not
-- per-call — a global cache read by 100 conversations is one storage bill,
-- not 100. This table is the single source of truth for that bill.
--
-- Cost is NOT stored on the row. Storage cost is computed on read using:
--
--   storage_cost =
--     (cached_tokens / 1e6)
--   * cost_per_million_cached_storage_per_hour            -- from llm_model_pricing
--   * EXTRACT(EPOCH FROM (
--       COALESCE(invalidated_at, LEAST(now(), expires_at)) - created_at
--     )) / 3600
--
-- That single formula covers alive (LEAST→now), invalidated (COALESCE→
-- invalidated_at), and TTL-expired (LEAST→expires_at) without a finalizer
-- cron. Provider eviction before TTL is unmodelled and slightly over-charges
-- ourselves — conservative and acceptable.
--
-- Creation cost is *per-call attributable* and lives on the per-call row in
-- llm_conversation_token_usage (cache_creation_tokens × creation rate). Not
-- duplicated here.

CREATE TABLE public.llm_cache_lifecycle (
  cache_name        text                        NOT NULL PRIMARY KEY,    -- provider's cache resource ID
  llm_provider      text                        NOT NULL,
  llm_model         text                        NOT NULL,
  scope             text                        NOT NULL,                -- 'global'|'tenant'|'account'|'conversation'
  tenant_id         uuid                        NULL,
  account_id        uuid                        NULL,
  conversation_id   uuid                        NULL,
  agent_name        text                        NULL,
  cached_tokens     bigint                      NOT NULL,
  created_at        timestamp without time zone NOT NULL DEFAULT now(),
  expires_at        timestamp without time zone NOT NULL,                -- created_at + TTL, immutable
  invalidated_at    timestamp without time zone NULL                     -- set on InvalidateCache; NULL while alive
);

COMMENT ON TABLE public.llm_cache_lifecycle IS
  'Lifecycle of provider-side prompt caches. One row per cache resource, all scopes. Storage cost is computed on read via JOIN to llm_model_pricing.';

CREATE INDEX idx_cache_lifecycle_account_created  ON public.llm_cache_lifecycle (account_id, created_at);
CREATE INDEX idx_cache_lifecycle_tenant_created   ON public.llm_cache_lifecycle (tenant_id, created_at);
CREATE INDEX idx_cache_lifecycle_conv_created     ON public.llm_cache_lifecycle (conversation_id, created_at);
CREATE INDEX idx_cache_lifecycle_alive            ON public.llm_cache_lifecycle (expires_at)
  WHERE invalidated_at IS NULL;
