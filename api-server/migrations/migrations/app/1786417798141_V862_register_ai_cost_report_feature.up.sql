-- Register the AI_COST_REPORT feature so it can be enabled per tenant via
-- feature_flag (whose feature_id FKs to feature.value). Gates the daily AI
-- cost digest cron (llm-server's RunAiCostDailyDigest) and the Cost Analyser
-- dashboard's Accounts tab (default OFF: no feature_flag row means disabled).
INSERT INTO "public"."feature"("description", "value")
VALUES ('AI/LLM cost report: daily + month-to-date Slack digest and dashboard Accounts tab', 'AI_COST_REPORT')
ON CONFLICT (value) DO NOTHING;
