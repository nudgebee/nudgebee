delete from
    auto_playbook_task apt
where
    execution_id in (
        select
            id
        from
            auto_playbook_executions ape
        where
            status = 'SCHEDULED'
    );
delete from
    auto_playbook_executions ape
where
    status = 'SCHEDULED';
