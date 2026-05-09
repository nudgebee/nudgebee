INSERT INTO
    "public"."runbook_action"(
        "attributes",
        "base_action_configs",
        "configs",
        "action_name",
        "description",
        "internal_identifier",
        "status",
        "created_at",
        "updated_at",
        "created_by",
        "library_id",
        "tenant_id"
    )
VALUES
    (
        '{
    "doc_link": "{{doc_url}}/help/docs/features/Autopilot/rest_api/",
    "resource_filter": {
        "resource_type": null,
        "context_applicable": true,
        "resource_mandatory": false,
        "resource_applicable": true
    },
    "applicable_trigger": [
        "event",
        "schedule"
    ],
    "applicable_event_type": []
}',
        '{}',
        '[
    {
        "name": "request_headers",
        "type": "textarea",
        "label": "API Headers",
        "required": false
    },
    {
        "name": "retries",
        "type": "textbox",
        "label": "retries"
    },
    {
        "name": "timeout_sec",
        "type": "textbox",
        "label": "timeout seconds",
        "required": false
    },
    {
        "name": "request_url",
        "type": "textbox",
        "label": "Config Map",
        "required": true
    },
    {
        "name": "payload",
        "type": "textbox",
        "label": "Payload",
        "required": true
    },
    {
        "name": "request_type",
        "type": "textbox",
        "label": "Request Type",
        "required": true
    },
    {
        "type":"checkbox",
        "label":"Synchronous",
        "name":"sync"
    }
    
]',
        'Rest API Call',
        'To make a REST API call',
        'rest_api',
        'ACTIVE',
        now(),
        null,
        null,
        '0e1e7e4c-b09c-4259-81dc-aec51ef45114',
        null
    );