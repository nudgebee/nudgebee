
INSERT INTO "public"."feature"("description", "value") VALUES (E'LLM Analyser cost/usage tab on the Optimise page', E'LLM_ANALYSER') ON CONFLICT (value) DO NOTHING;
