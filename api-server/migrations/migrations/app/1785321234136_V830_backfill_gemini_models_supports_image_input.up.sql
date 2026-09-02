-- Backfills llm_model_pricing.supports_image_input (added in V829) for the
-- generative Gemini models. IsVisionCapableModel() now defaults to false
-- (deny) unless a model has an explicit true row, so without this backfill
-- no model would be treated as vision-capable and image-driven investigation
-- would never run for any account.
--
-- Excludes embedding models (gemini-embedding, gemini-embedding-001,
-- models/gemini-embedding-001) — those never take image/chat input in this
-- flow and are left NULL (unknown), not marked either true or false.

UPDATE public.llm_model_pricing
SET supports_image_input = true
WHERE provider_name = 'googleai'
  AND model_name ILIKE '%gemini%'
  AND model_name NOT ILIKE '%embedding%';
