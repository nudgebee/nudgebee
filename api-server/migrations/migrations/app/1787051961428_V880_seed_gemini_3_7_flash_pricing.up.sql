-- gemini-3.7-flash shipped after the V878 catalog seed, so live traffic on it
-- (1.7k calls/30d in dev) records $0 cost and falls to the 4096 output floor.
-- Rates copied from gemini-3.6-flash (Google prices the flash tier uniformly);
-- ceiling matches the rest of the gemini-3 family (#36449).
INSERT INTO llm_model_pricing (
    model_name, provider_name, tenant_id,
    cost_per_million_input_tokens, cost_per_million_output_tokens,
    cost_per_million_cached_input_tokens, cost_per_million_cache_creation_tokens,
    max_output_tokens
)
SELECT 'gemini-3.7-flash', 'googleai', NULL, 1.5, 7.5, 0.15, 0, 65536
WHERE NOT EXISTS (
    SELECT 1 FROM llm_model_pricing
    WHERE tenant_id IS NULL AND provider_name = 'googleai' AND model_name = 'gemini-3.7-flash'
);
