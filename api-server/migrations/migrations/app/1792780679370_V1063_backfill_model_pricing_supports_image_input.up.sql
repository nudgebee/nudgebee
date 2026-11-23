-- Record image support for the built-in Gemini, Claude and OpenAI chat models.
--
-- The vision gate (llm-server IsVisionCapableModel) refuses image attachments
-- unless supports_image_input is true for the model. Only V830 ever set it, and only
-- for the Gemini rows that existed then. Every built-in row seeded since (V880
-- gemini-3.7-flash, V984 gemini-3.8-flash and the GPT-5.6 / GPT-6 rows, V1038) left
-- it NULL, as did every Claude and OpenAI row, so chats on the default model and on
-- every Claude or GPT model answered "The current model doesn't support image
-- attachments".
--
-- Each family was checked against the provider's own model docs (2026-10-24):
--   - Google: every Gemini chat model takes images; embedding models do not.
--   - Anthropic: every Claude model takes text and image input, including Mythos.
--   - OpenAI: each model below lists "Input modalities: text, image". OpenAI rows not
--     listed here (embeddings) are left alone.
-- Other built-in rows (Bedrock Llama, custom Gemma / Nemotron, Qwen, the Cloudflare
-- and Typesafe decision models) stay NULL until someone confirms them.
--
-- Built-in rows only, and only where the flag is still NULL, so an explicit false and
-- tenant rows are never touched and a re-run is a no-op.
--
-- Lock window: row locks on at most ~80 llm_model_pricing rows.
UPDATE llm_model_pricing
SET supports_image_input = true
WHERE tenant_id IS NULL
  AND supports_image_input IS NULL
  AND (
        (provider_name = 'googleai' AND model_name ILIKE '%gemini%' AND model_name NOT ILIKE '%embedding%')
     OR (provider_name = 'anthropic' AND model_name LIKE 'claude-%')
     OR (provider_name = 'openai' AND model_name IN (
            'gpt-4o', 'gpt-4o-mini',
            'gpt-5', 'gpt-5-mini', 'gpt-5-nano',
            'gpt-5.4', 'gpt-5.4-mini', 'gpt-5.4-nano', 'gpt-5.4-pro',
            'gpt-5.5', 'gpt-5.5-pro',
            'gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna',
            'gpt-6-sol', 'gpt-6-luna', 'gpt-6-astra', 'gpt-6.1-sol',
            'o3', 'o4-mini'))
  );
