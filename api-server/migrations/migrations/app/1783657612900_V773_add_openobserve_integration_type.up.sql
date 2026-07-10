
INSERT INTO "public"."integration_types"("category", "description", "name") VALUES (E'observability_platform', null, E'openobserve') ON CONFLICT ("name") DO NOTHING;
