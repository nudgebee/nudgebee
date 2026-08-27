-- Restores the catalog rows and the two workflow metering dimensions.
-- Per-tenant feature_flag / billing_plan_features rows are data; restore from backup.

INSERT INTO "public"."feature"("value", "description") VALUES
    ('WORKFLOWS', 'Workflow automation feature'),
    ('WORKFLOW_TEMPLATES', 'Workflow templates')
ON CONFLICT ("value") DO NOTHING;

INSERT INTO "public"."billing_feature_mapping" ("feature_id", "dimension", "aws_metered_dimension", "overage_rate", "included_limit_default", "description") VALUES
    ('WORKFLOWS', 'workflow_executions', 'ai_workflow', 0.50, 6000, 'Workflow executions per month'),
    ('WORKFLOWS', 'ai_workflow_steps', 'ai_workflow_function_call', 1.00, 500, 'AI-powered workflow steps per month')
ON CONFLICT ("dimension") DO NOTHING;
