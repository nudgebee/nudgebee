-- Register the llm_gateway integration type so integrations.type (FK →
-- integration_types.name) accepts it. This is the AI Gateway's own provider-credential
-- surface ("LLM Gateway"), grouped under the LLM category alongside the llm type.
INSERT INTO "public"."integration_types" ("category", "description", "name")
VALUES (E'LLM', E'Configure AI Gateway provider credentials', E'llm_gateway')
ON CONFLICT ("name") DO NOTHING;
