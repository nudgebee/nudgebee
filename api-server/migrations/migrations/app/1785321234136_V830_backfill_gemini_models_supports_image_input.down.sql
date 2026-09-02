UPDATE public.llm_model_pricing
SET supports_image_input = NULL
WHERE provider_name = 'googleai'
  AND model_name ILIKE '%gemini%'
  AND model_name NOT ILIKE '%embedding%';
