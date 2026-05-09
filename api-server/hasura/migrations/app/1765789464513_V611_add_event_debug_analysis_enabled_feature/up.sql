
INSERT INTO "public"."feature"("description", "value") VALUES (E'Enable debug analysis for events at account level', E'EVENT_DEBUG_ANALYSIS_ENABLED') ON CONFLICT (value) DO NOTHING;
