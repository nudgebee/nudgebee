-- Both features shipped and the frontend gates are gone, so these rows decide nothing.
-- Dependants go before the parent: every FK to feature(value) is ON DELETE RESTRICT/NO ACTION.

DELETE FROM "public"."feature_flag" WHERE "feature_id" IN ('WORKFLOWS', 'WORKFLOW_TEMPLATES');

DELETE FROM "public"."billing_feature_mapping" WHERE "feature_id" IN ('WORKFLOWS', 'WORKFLOW_TEMPLATES');

DELETE FROM "public"."billing_plan_features" WHERE "feature_id" IN ('WORKFLOWS', 'WORKFLOW_TEMPLATES');

DELETE FROM "public"."feature" WHERE "value" IN ('WORKFLOWS', 'WORKFLOW_TEMPLATES');
