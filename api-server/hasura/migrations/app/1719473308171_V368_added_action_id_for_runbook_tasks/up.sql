update
    auto_playbook_task as bck
set
    action_id = (
        select
            id
        from
            runbook_action
        where
            created_by is null
            and internal_identifier = bck.task_type
    );
