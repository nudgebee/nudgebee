alter table "public"."llm_model_pricing" 
add column "cache_cost_per_million_tokens_per_hour" real not null default 0;

comment on column "public"."llm_model_pricing"."cache_cost_per_million_tokens_per_hour" is E'in USD($)';

UPDATE llm_model_pricing 
SET cache_cost_per_million_tokens_per_hour = CASE
    WHEN model_name = 'gemini-2.5-pro' THEN 4.50
    WHEN model_name = 'gemini-2.5-flash' THEN 1.00
    WHEN model_name = 'gemini-2.5-flash-preview-09-2025' THEN 1.00
    WHEN model_name = 'gemini-2.5-flash-lite' THEN 1.00
    WHEN model_name = 'gemini-2.5-flash-lite-preview-06-17' THEN 1.00
    WHEN model_name = 'gemini-2.5-flash-lite-preview-09-2025' THEN 1.00
    WHEN model_name = 'gemini-2.0-flash' THEN 1.00
    WHEN model_name = 'gemini-2.0-flash-001' THEN 1.00
    WHEN model_name = 'gemini-2.0-flash-lite' THEN 0
    WHEN model_name = 'gemini-1.5-pro' THEN 4.50
    WHEN model_name = 'gemini-1.5-flash' THEN 1.00
    WHEN model_name = 'gemini-1.5-flash-8b' THEN 0.25
    WHEN model_name = 'gemini-embedding' THEN 0
    ELSE 0
END
WHERE provider_name = 'googleai';