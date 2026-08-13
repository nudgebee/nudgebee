-- AI Gateway: capture the PROVIDER-RESPONDED model (the id the provider echoes back,
-- e.g. a dated snapshot gpt-4o-mini-2024-07-18) alongside requested + resolved, closing
-- the requested->resolved->responded audit loop and exposing silent snapshot/alias drift.
-- Idempotent; not priced on (pricing stays on the resolved/normalized model).
ALTER TABLE llm_gateway_usage ADD COLUMN IF NOT EXISTS responded_model text NOT NULL DEFAULT '';
