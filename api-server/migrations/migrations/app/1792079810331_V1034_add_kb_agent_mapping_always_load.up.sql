-- #40899: a skill linked to an agent can be marked "always load" — its full
-- text then goes into that agent's prompt on every run instead of waiting to
-- be found by search. Defaults to false so existing links keep their current
-- search-only behaviour. Adding a NOT NULL column with a constant default is a
-- metadata-only change in Postgres 11+ (no table rewrite).
ALTER TABLE llm_kb_agent_mappings
    ADD COLUMN IF NOT EXISTS always_load BOOLEAN NOT NULL DEFAULT FALSE;
