-- Revert the claude-opus-5 price row added by the up migration. It had no prior row
-- (this migration filled the gap), so a plain delete restores the previous state.
DELETE FROM llm_model_pricing
 WHERE provider_name = 'anthropic'
   AND model_name = 'claude-opus-5';
