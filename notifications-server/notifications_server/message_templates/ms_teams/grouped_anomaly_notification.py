from collections import defaultdict
from typing import List, Dict, Any, Optional
from datetime import datetime

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.base import Emojis
from notifications_server.message_templates.slack.grouped_anomaly_notification import AnomalyAlertParams

MAX_CARD_TEXT_LENGTH = 3800


def truncate_text(text: str, max_len: int = MAX_CARD_TEXT_LENGTH) -> str:
    return text if len(text) <= max_len else text[: max_len - 3] + "..."


def get_grouped_anomaly_alerts_ms_teams_template(input_data: Any) -> Dict[str, Any]:
    if not input_data:
        return {
            "type": "AdaptiveCard",
            "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
            "version": "1.0",
            "body": [{"type": "TextBlock", "text": "No Anomaly Alerts to report", "wrap": True}],
        }

    if hasattr(input_data, "events"):
        alerts: List[AnomalyAlertParams] = input_data.events
    elif isinstance(input_data, list):
        alerts = input_data
    else:
        raise TypeError("Expected AnomalyAlertSummaryParams or list[AnomalyAlertParams]")

    grouped: Dict[str, List[AnomalyAlertParams]] = defaultdict(list)
    for alert in alerts:
        grouped[alert.cloud_account_id].append(alert)

    body: List[Dict[str, Any]] = [
        {
            "type": "TextBlock",
            "text": f"{Emojis.Alert.value} **{len(alerts)} Anomalies detected across {len(grouped)} accounts**",
            "size": "Large",
            "weight": "Bolder",
            "wrap": True,
        },
        {"type": "TextBlock", "text": " ", "separator": True},
    ]

    for cloud_account_id, acct_alerts in grouped.items():
        cluster_name = acct_alerts[0].cluster or "Cluster"
        account_url = f"{public_ip()}/kubernetes/details/{cloud_account_id}?tab=2&subtab=6#events/anomaly"

        body.append(
            {
                "type": "TextBlock",
                "text": f"**Account:** [{cluster_name}]({account_url})",
                "weight": "Bolder",
                "wrap": True,
                "spacing": "Medium",
            }
        )

        for alert in acct_alerts:
            try:
                ts = int(datetime.fromisoformat(alert.starts_at.replace("Z", "")).timestamp())
                human = datetime.fromtimestamp(ts).strftime("%d %b %Y %I:%M %p")
            except ValueError:
                human = alert.starts_at

            title = alert.title or f"{alert.subject_name} anomaly"

            body.extend(
                [
                    {
                        "type": "TextBlock",
                        "text": f"- **{title}**",
                        "wrap": True,
                    },
                    {
                        "type": "TextBlock",
                        "text": f"  Namespace: *{alert.subject_namespace}* | Type: *{alert.subject_type or 'N/A'}*",
                        "wrap": True,
                    },
                    {
                        "type": "TextBlock",
                        "text": f"  Priority: *{alert.priority}* | Status: *{alert.status}*",
                        "wrap": True,
                    },
                    {
                        "type": "TextBlock",
                        "text": f"  Started at: {human}",
                        "wrap": True,
                    },
                ]
            )
        body.append({"type": "TextBlock", "text": " ", "separator": True})

    body.append(
        {
            "type": "TextBlock",
            "text": (
                f"{Emojis.Event.value} Review detected anomalies and investigate impacted workloads in your clusters."
            ),
            "wrap": True,
            "spacing": "Medium",
        }
    )

    card_payload: Dict[str, Any] = {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.4",
        "msteams": {"width": "Full"},
        "body": body,
    }

    return card_payload
