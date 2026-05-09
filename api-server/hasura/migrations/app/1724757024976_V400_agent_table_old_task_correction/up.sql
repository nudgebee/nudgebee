update
    agent_task
set
    status = 'Timeout'
where
    created_at < (CURRENT_TIMESTAMP - INTERVAL '3600 seconds')
    and status = 'TODO';
