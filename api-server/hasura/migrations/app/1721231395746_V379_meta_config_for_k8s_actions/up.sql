update
    runbook_action
set
    configs = '[
{
    "type":"textarea",
    "required": true,
    "label":"Message",
    "name":"message",
    "rows":6,
    "doc":"Variables: \ntrigger_type\napplicable_resources\nnotify_time\ntrigger_condition\nrunbook_name\nrunbook_link"
},
{
    "type":"checkbox",
    "label":"Slack",
    "name":"slack_enabled"
},
{
    "type":"dropdown",
    "label":"Select Channel",
    "name":"channel_name"
},
{
    "type":"checkbox",
    "label":"MS Teams",
    "name":"mas_teams_enabled"
},
{
    "type":"dropdown",
    "label":"Teams",
    "name":"team_name"
},
{
    "type":"dropdown",
    "label":"Channels",
    "name":"channel_name"
},
{
    "type":"checkbox",
    "label":"Google Chat",
    "name":"google_chat_enabled"
},
{
    "type":"checkbox",
    "label":"Google Channel",
    "name":"channel_name"
}
]'
where
    internal_identifier = 'notification';

update
    runbook_action
set
    configs = '[
    {
        "type":"textbox",
        "required": true,
        "label":"Image",
        "name":"image"
    },
    {
        "type":"dropdown",
        "label":"Image Pull Policy",
        "name":"image_pull_policy",
        "options":["Always","Never","ifNotPresent"]
    },
    {
        "type":"textbox",
        "required": true,
        "label":"Secret",
        "name":"secret"
    },
    {
        "type":"textbox",
        "required": true,
        "label":"Config Map",
        "name":"config_map_name"
    }
]'
where
    internal_identifier = 'custom_image_execute';

update
    runbook_action
set
    configs = '[
{
    "type":"textarea",
    "required": true,
    "label":"Description",
    "name":"description",
    "rows":6,
    "doc":"Variables: \ntrigger_type\napplicable_resources\nnotify_time\ntrigger_condition\nrunbook_name\nrunbook_link"
},
{
    "type":"checkbox",
    "label":"Jira",
    "name":"jira_enabled"
},
{
    "type":"dropdown",
    "label":"Select Project",
    "name":"project_key"
},
{
    "type":"dropdown",
    "label":"Select Priority",
    "name":"severity"
},
{
    "type":"textbox",
    "label":"Enter Assignee Email",
    "name":"assignee"
},
{
    "type":"dropdown",
    "label":"Select Project Config",
    "name":"configuration_id"
}
]'
where
    internal_identifier = 'ticket_create';

update
    runbook_action
set
    configs = '[
{
    "type":"textarea",
    "required": true,
    "label":"Enter Shell Script",
    "name":"command",
    "rows":3
},
{
    "type":"textbox",
    "label":"busy box",
    "name":"image"
},
{
    "type":"toggle",
    "true_label":"Ephemeral Container",
    "false_label":"Dedicated Pod",
    "name":"ephemeral"
}
]'
where
    internal_identifier = 'k8s_bash';

update
    runbook_action
set
    configs = '[
    {
        "type":"toggle",
        "true_label":"Dedicated Pod",
        "false_label":"Dedicated Pod",
        "name":""
    },
    {
        "type":"checkbox",
        "label":"CPU",
        "name":""
    },
    {
        "type":"checkbox",
        "label":"Remove Limit",
        "name":""
    },
    {
        "type":"checkbox",
        "label":"Memory",
        "name":""
    },
    {
        "type":"textbox",
        "label":"Increase",
        "name":""
    },
    {
        "type":"textbox",
        "label":"Minimum",
        "name":""
    },
    {
        "type":"textbox",
        "label":"Maximum",
        "name":""
    },
    {
        "type":"textbox",
        "label":"Decrease",
        "name":""
    },
    {
        "type":"textbox",
        "label":"Minimum",
        "name":""
    },
    {
        "type":"textbox",
        "label":"Maximum",
        "name":""
    }
]'
where
    internal_identifier = 'vertical_rightsize';

update
    runbook_action
set
    configs = '[
    {
        "type":"toggle",
        "true_label":"Scale Up",
        "false_label":"Scale Down",
        "name":"scale_up"
    },
    {
        "type":"textbox",
        "label":"Maximum",
        "name":"max"
    },
    {
        "type":"textbox",
        "label":"Minimum",
        "name":"min"
    },
    {
        "type":"toggle",
        "true_label":"Absolute",
        "false_label":"Rate",
        "name":"absolute"
    },
    {
        "type":"textbox",
        "label":"Change Replica By",
        "name":"change_by"
    },
    {
        "type":"textbox",
        "label":"Change Replica to",
        "name":"change_to"
    }
]'
where
    internal_identifier = 'horizontal_rightsize';

update
    runbook_action
set
    configs = '[
    {
        "type":"textbox",
        "label":"Change By",
        "name":"change_by"
    },
    {
        "type":"textbox",
        "label":"Change To",
        "name":"change_to"
    },
    {
        "type":"textbox",
        "label":"Enter PV Name",
        "name":"name"
    }   
]'
where
    internal_identifier = 'pv_rightsize';

update
    runbook_action
set
    configs = '[
    {
        "type":"textbox",
        "label":"Cron Expression",
        "name":"scale_up_cron"
    },
    {
        "type":"textbox",
        "label":"Scale Down Replica",
        "name":"scale_down_replica"
    }
]'
where
    internal_identifier = 'workload_scalar';