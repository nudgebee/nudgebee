insert into
    public.runbook_action (
        id,
        action_name,
        created_at,
        created_by,
        library_id,
        configs,
        "attributes",
        base_action_configs,
        updated_at,
        status,
        description,
        tenant_id,
        internal_identifier,
        account_type
    )
values
    (
        gen_random_uuid(),
        'eks start',
        now(),
        null,
        '0e1e7e4c-b09c-4259-81dc-aec51ef45114' :: uuid,
        '[]' :: jsonb,
        '{"resource_filter": {"resource_type": "cluster", "context_applicable": true, "resource_mandatory": true, "resource_applicable": false}, "applicable_trigger": ["schedule"], "applicable_event_type": []}' :: jsonb,
        '{}' :: jsonb,
        null,
        'DRAFT',
        'To start EKS clsuter',
        null,
        'aws_eks_start',
        'AWS'
    ) ON CONFLICT(action_name, library_id) DO NOTHING;