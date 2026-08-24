-- Which configured LLM slot a call resolved through ({layer}:{scope}[:{name}],
-- e.g. env:global or db:<uuid>:tier:summary). llm_provider/llm_model alone cannot
-- distinguish two configs that share a provider and model, so usage views could
-- not attribute a call to the integration that served it.
ALTER TABLE public.llm_conversation_token_usage
    ADD COLUMN IF NOT EXISTS llm_config_source VARCHAR(255);
