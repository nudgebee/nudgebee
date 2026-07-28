-- Restore the Knowledge Graph feature definition (matches the description set by
-- V664). The per-tenant feature_flag rows cannot be restored — they were no-ops
-- once the flag was ungated, so nothing depends on their exact prior state.
INSERT INTO "public"."feature" ("value", "description")
VALUES ('TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH', 'Enable Knowledge Graph')
ON CONFLICT ("value") DO NOTHING;
