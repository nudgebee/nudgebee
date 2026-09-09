-- Restores the feature row only. The tenant feature_flag rows are not recreated:
-- nothing read them, so their loss is not a behaviour change to roll back.
INSERT INTO "public"."feature"("description", "value") VALUES ('Enable automated pr raise for code analysis fixes generated', 'LLM_CODE_ANALYSIS_RAISE_PR') ON CONFLICT DO NOTHING;
