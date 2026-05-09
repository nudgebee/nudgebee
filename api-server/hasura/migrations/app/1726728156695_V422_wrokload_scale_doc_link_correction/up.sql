update
    runbook_action
set
    attributes = '{
    "doc_link": "{{doc_url}}/help/docs/features/Autopilot/Actions/workload_scaler/",
    "resource_filter": {
        "resource_type": "workload",
        "multiple_resource": false,
        "context_applicable": false,
        "resource_mandatory": true,
        "resource_applicable": true
    },
    "applicable_trigger": [
        "event"
    ],
    "applicable_event_type": []
}'
where
    internal_identifier = 'workload_scalar'