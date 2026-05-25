
INSERT INTO "public"."integration_types"("category", "description", "name") VALUES (E'observability_platform', null, E'datadog') ON CONFLICT ("name") DO NOTHING;

