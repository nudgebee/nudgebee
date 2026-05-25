
update
    auto_playbook_task
set
    meta_data = '{}'
where
    meta_data -> 'message' is not null
    and meta_data ->> 'message' = '';
