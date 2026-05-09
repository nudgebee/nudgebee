UPDATE
    auto_playbook_executions
SET
    attribute = jsonb_set(
        attribute :: jsonb,
        '{runbook_account_type}',
        '"K8s"'
    );

UPDATE
    auto_playbook_task
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{runbook_account_type}',
        '"K8s"'
    );

UPDATE
    auto_playbook
SET
    attributes = jsonb_set(
        attributes :: jsonb,
        '{runbook_account_type}',
        '"K8s"'
    );
