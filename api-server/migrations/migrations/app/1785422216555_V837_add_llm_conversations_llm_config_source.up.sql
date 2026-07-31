-- Pins a conversation to one configured LLM slot (endpoint / api-key / api-type /
-- api-version / region) so every call in the request tree resolves to the same
-- place instead of drifting per tier. Value is the wire-format source id sent as
-- NBQueryConfig.llm_config_source — {layer}:{scope}[:{name}], e.g. 'env:tier:summary'
-- or 'db:<integration-uuid>'.
--
-- Sits alongside llm_provider / llm_model / llm_tier_overrides, which record the
-- same class of thing: an explicit user selection that stays sticky for the
-- conversation. Orthogonal to those — a conversation can pin a slot with or
-- without also pinning a provider/model, so this column is NOT cleared by the
-- blanket/tier mutual-exclusivity updates.
ALTER TABLE llm_conversations
    ADD COLUMN IF NOT EXISTS llm_config_source text;
