from notifications_server.configs.settings import public_ip


def get_slack_resolved_finding_message_template(alert_name, reported_at, resource_name, finding_id):
    header_blocks = [
        {"type": "section", "text": {"type": "mrkdwn", "text": f"Alert *{alert_name}*  is Resolved"}},
        {
            "type": "section",
            "fields": [
                {"type": "mrkdwn", "text": f"*Time:* {reported_at}"},
                {"type": "mrkdwn", "text": f"*Resource:* {resource_name}"},
            ],
        },
        {
            "type": "actions",
            "elements": [
                {
                    "type": "button",
                    "text": {"type": "plain_text", "text": "See Alert Details"},
                    "value": "click_me_123",
                    "action_id": "nudgebee-redirect",
                    "url": f"{public_ip()}/investigate?id={finding_id}&utm=slack",
                }
            ],
        },
    ]
    return {
        "text": alert_name,
        "blocks": header_blocks,
        "unfurl_links": False,
    }
