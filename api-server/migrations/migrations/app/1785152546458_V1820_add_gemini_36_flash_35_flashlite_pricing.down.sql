-- Revert the two Gemini tier-target price rows added by the up migration. These
-- models had no prior row (they were the gap this migration filled), so a plain
-- delete restores the previous state.
DELETE FROM llm_model_pricing
 WHERE provider_name = 'googleai'
   AND model_name IN ('gemini-3.6-flash', 'gemini-3.5-flash-lite');
