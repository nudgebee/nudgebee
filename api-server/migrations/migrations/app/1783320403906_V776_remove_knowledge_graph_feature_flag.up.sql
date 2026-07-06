-- Knowledge Graph is now built and shown for every tenant unconditionally.
-- Remove the TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH feature flag end-to-end.
-- feature_flag.feature_id is a FK to feature.value (ON DELETE restrict), so the
-- referencing rows must be deleted before the feature row.
DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH';
DELETE FROM "public"."feature" WHERE "value" = 'TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH';
