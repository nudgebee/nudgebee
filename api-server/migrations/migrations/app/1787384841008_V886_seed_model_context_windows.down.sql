-- Revert to "unknown", which sends llm-server back to its code model map.
-- Scoped to built-in rows: the one tenant-scoped row that carried a context
-- window before this migration was not set by it and must survive the rollback.
UPDATE llm_model_pricing
SET max_context_tokens = NULL
WHERE tenant_id IS NULL;

-- Remove the self-hosted rows this migration introduced. Guarded on the zero
-- pricing it inserted so a row later given real costs is not dropped.
DELETE FROM llm_model_pricing
WHERE tenant_id IS NULL
  AND cost_per_million_input_tokens = 0
  AND cost_per_million_output_tokens = 0
  AND model_name IN (
    'Qwen/Qwen3.6-35B-A3B-FP8',
    'google/gemma-4-26B-A4B-it',
    'NVIDIA-Nemotron-3-Nano-30B-A3B-BF16',
    'google/gemma-4-31b-it');
