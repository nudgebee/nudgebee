-- Register the EVENT_AUTO_RAISE_PR_ENABLED feature so it can be enabled per
-- tenant/account via feature_flag (whose feature_id FKs to feature.value).
-- Gates the automatic event->code-fix PR path in llm-server (default OFF: no
-- feature_flag row means disabled).
INSERT INTO "public"."feature"("description", "value")
VALUES ('Automatically raise a code-fix PR when an event log analysis localizes a code-level root cause', 'EVENT_AUTO_RAISE_PR_ENABLED')
ON CONFLICT (value) DO NOTHING;
