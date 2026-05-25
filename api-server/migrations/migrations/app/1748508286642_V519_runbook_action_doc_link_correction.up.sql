UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/workload_scaler"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'workload_scalar';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/horizontal_rightsize"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'horizontal_rightsize';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/node_shutdown"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'k8s_node_graceful_shutdown';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/vertical_rightsize"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'vertical_rightsize';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"https://docs.nudgebee.com/docs/features/autopilot/auto_runbook/workload_restart/"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'workload_restart';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/execute_bash"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'k8s_bash';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/rest_api"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'rest_api';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/notify"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'notification';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/pvc_rightsize"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'pv_rightsize';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/create_ticket"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'ticket_create';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/delete_pod_gracefully"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'pod_delete';

UPDATE
    runbook_action
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{doc_link}',
        '"{{doc_url}}/docs/features/autopilot/auto_runbook/execute_custom_image"'
    )
WHERE
    created_by is null
    and account_type = 'K8s'
    and internal_identifier = 'custom_image_execute';