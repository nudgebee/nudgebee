
INSERT INTO "public"."feature"("description", "value") VALUES ('Enable automated pr raise for code analysis fixes generated', 'LLM_CODE_ANALYSIS_RAISE_PR') ON CONFLICT DO NOTHING;
