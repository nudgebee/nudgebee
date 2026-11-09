-- Remove only the rows no install had before V1038. The re-seeded rows already existed wherever
-- V791/V792 ran, and deleting them would un-price those installs, so they stay.
DELETE FROM llm_model_pricing
 WHERE tenant_id IS NULL AND provider_name = 'anthropic'
   AND model_name IN ('claude-haiku-5-5', 'claude-mythos-5-1', 'claude-mythos-5',
                      'claude-opus-4-5-20251101', 'claude-sonnet-4-5-20250929',
                      'claude-sonnet-4-0', 'claude-sonnet-4-20250514', 'claude-opus-4-0');
