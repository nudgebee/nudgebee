DO $$
DECLARE
    tbl text;
BEGIN
    FOREACH tbl IN ARRAY ARRAY[
        'application_group_mapping', 'auto_optimize_resource_map', 'auto_pilot', 'auto_pilot_approval_policy',
        'auto_pilot_approvals', 'auto_pilot_task', 'auto_playbook', 'auto_playbook_executions', 'auto_playbook_task',
        'billing_usage_cost', 'configuration_store', 'dashboard_bindings', 'dashboards', 'dw_queries',
        'event_incoming_webhooks', 'feature_flag', 'integration_user_accounts', 'integrations_cloud_accounts',
        'llm_conversations', 'llm_functions', 'llm_global_contexts', 'llm_kb_account_overrides', 'llm_knowledgebases',
        'llm_rags', 'notification_channel_account_mappings', 'notification_rule_accounts', 'notification_rules',
        'ownership_rules', 'resource_owners', 'runbook_task_output', 'upgrade_plan', 'upgrade_plan_audit',
        'upgrade_plan_steps', 'user_history', 'workflow_templates', 'workflows'
    ]
    LOOP
        IF to_regclass('public.' || quote_ident(tbl)) IS NOT NULL THEN
            EXECUTE format('ALTER TABLE public.%I DROP CONSTRAINT IF EXISTS %I', tbl, tbl || '_account_tenant_fkey');
        END IF;
    END LOOP;
END $$;
