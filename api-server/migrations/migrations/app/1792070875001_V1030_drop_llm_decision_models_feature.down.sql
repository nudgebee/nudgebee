INSERT INTO "public"."feature"("value", "display_name", "category", "description")
VALUES (
    'LLM_DECISION_MODELS',
    'LLM Decision Models',
    'other',
    'Internal: decision-model settings in the LLM configuration (beta)'
)
ON CONFLICT (value) DO NOTHING;
