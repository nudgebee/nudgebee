-- Registers the LLM_DECISION_MODELS feature (#40902) so tenant feature_flag rows
-- can reference it. It gates the "Decision models (beta)" section of the LLM
-- configuration screen: decision inputs go to a new provider (Cloudflare), so
-- the section stays hidden until a tenant is explicitly enabled. No tenant row
-- is created; the default 'opt_in' polarity means no row = disabled. Same
-- register pattern as V996. Idempotent.

INSERT INTO "public"."feature"("value", "display_name", "category", "description")
VALUES (
    'LLM_DECISION_MODELS',
    'LLM Decision Models',
    'other',
    'Internal: decision-model settings in the LLM configuration (beta)'
)
ON CONFLICT (value) DO NOTHING;
