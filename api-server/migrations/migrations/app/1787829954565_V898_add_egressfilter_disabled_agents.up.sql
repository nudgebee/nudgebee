-- Per-tenant agents whose payloads skip secret detection. Excludes by
-- PRODUCER -> reaches noise no rule can. Default '[]' = today's behaviour.
ALTER TABLE "public"."llm_egressfilter_tenant_config"
    ADD COLUMN IF NOT EXISTS "disabled_agents" JSONB NOT NULL DEFAULT '[]'::jsonb;
