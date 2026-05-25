update
    runbook_action
set
    attributes = '{
    "account_type":"AWS",
    "applicable_trigger": ["event","schedule"],
    "applicable_event_type": ["oom","cluster_down"],
    "resource_filter":{
        "resource_type":"namespace",
        "context_applicable":true,
        "resource_mandatory":true,
        "resource_applicable":false
    }
}';

update
    runbook_action
set
    attributes = jsonb_set(
        attributes,
        '{resource_filter, resource_mandatory}',
        'false'
    )
where
    id in (
        'b26a6c40-5479-454d-8f2f-02aa97a18cc4',
        'b8afc8d3-6845-4073-9c54-518199ab7251'
    );

update
    runbook_action
set
    attributes = jsonb_set(
        attributes,
        '{resource_filter, resource_applicable}',
        'false'
    )
where
    id in (
        'b26a6c40-5479-454d-8f2f-02aa97a18cc4',
        'b8afc8d3-6845-4073-9c54-518199ab7251'
    );