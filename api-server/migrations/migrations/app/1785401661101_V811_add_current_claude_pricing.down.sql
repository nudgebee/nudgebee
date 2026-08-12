-- Remove only the rows this migration newly introduces. sonnet-4.6 and haiku-4.5 are
-- owned by V790 (upserted here at identical rates), so they are intentionally NOT
-- deleted on rollback.
DELETE FROM llm_model_pricing
WHERE provider_name = 'anthropic'
  AND model_name IN (
    'claude-fable-5',
    'claude-opus-4.8', 'claude-opus-4.7', 'claude-opus-4.6', 'claude-opus-4.5',
    'claude-sonnet-5', 'claude-sonnet-4.5'
  );
