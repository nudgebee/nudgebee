from collections import defaultdict
from typing import List, Dict, Any, Optional
from datetime import datetime
from pydantic import BaseModel
from notifications_server.configs.settings import public_ip

MAX_BLOCKS = 50
MAX_TEXT_LENGTH = 2900
MAX_ALERTS_PER_ACCOUNT = 8


class AnomalyAlertParams(BaseModel):
    id: str
    title: str
    source: str
    priority: str
    status: str
    subject_name: str
    subject_namespace: str
    starts_at: str
    finding_id: str
    cluster: str
    cloud_account_id: str
    updated_at: Optional[str] = None
    subject_type: Optional[str] = None
    subject_owner: Optional[str] = None


class AnomalyAlertSummaryParams(BaseModel):
    events: List[AnomalyAlertParams]


def get_anomaly_aggregated_message_params(events: List[Dict[str, Any]]) -> AnomalyAlertSummaryParams:
    return AnomalyAlertSummaryParams(events=[AnomalyAlertParams(**e) for e in events])


def truncate_text(text: str, max_len: int = MAX_TEXT_LENGTH) -> str:
    return text if len(text) <= max_len else text[: max_len - 3] + "..."


def create_account_attachment(cloud_account_id: str, acct_alerts: List[AnomalyAlertParams]) -> Dict[str, Any]:
    """Create a collapsible attachment for account anomalies."""
    cluster_name = acct_alerts[0].cluster or "Cluster"
    account_url = f"{public_ip()}/kubernetes/details/{cloud_account_id}?tab=2&subtab=6#events/anomaly"

    display_alerts = acct_alerts[:MAX_ALERTS_PER_ACCOUNT]
    lines = []
    add_alerts(display_alerts, lines)

    if len(acct_alerts) > MAX_ALERTS_PER_ACCOUNT:
        lines.append(f"…and {len(acct_alerts) - MAX_ALERTS_PER_ACCOUNT} more anomalies in this account.")

    text_block = truncate_text("\n".join(lines).strip())

    blocks = [
        {"type": "section", "text": {"type": "mrkdwn", "text": f"*Account:* <{account_url}|{cluster_name}>"}},
        {"type": "section", "text": {"type": "mrkdwn", "text": text_block}},
    ]

    return {
        "color": "#ff9800",
        "fallback": f"{cluster_name}: {len(acct_alerts)} anomalies detected",
        "blocks": blocks,
    }


def get_grouped_anomaly_alerts_template(input_data: List[AnomalyAlertParams]) -> Dict[str, Any]:
    if isinstance(input_data, AnomalyAlertSummaryParams):
        alerts: List[AnomalyAlertParams] = input_data.events
    else:
        alerts = input_data

    # Group anomalies by cloud account ID
    grouped: Dict[str, List[AnomalyAlertParams]] = defaultdict(list)
    for alert in alerts:
        grouped[alert.cloud_account_id].append(alert)

    total_alerts = len(alerts)
    total_accounts = len(grouped)

    # Header blocks
    blocks: List[Dict[str, Any]] = [
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": f"*{total_alerts} Anomalies detected across {total_accounts} accounts*",
            },
        },
        {"type": "divider"},
    ]

    # Create collapsible attachments for each account
    attachments = []
    for cloud_account_id, acct_alerts in grouped.items():
        attachments.append(create_account_attachment(cloud_account_id, acct_alerts))

    # Footer
    blocks.append(
        {
            "type": "context",
            "elements": [
                {
                    "type": "mrkdwn",
                    "text": "Review detected anomalies and investigate impacted workloads in your clusters.",
                }
            ],
        }
    )

    return {
        "text": "Grouped Anomaly Alert Summary",
        "blocks": blocks,
        "attachments": attachments[:20],  # Slack limit
        "unfurl_links": False,
    }


def add_alerts(display_alerts, lines):
    for alert in display_alerts:
        try:
            ts = int(datetime.fromisoformat(alert.starts_at.replace("Z", "")).timestamp())
            fallback = datetime.fromtimestamp(ts).strftime("%d %b %Y %I:%M %p")
            date_block = f"<!date^{ts}^{{date_short_pretty}} {{time}}|{fallback}>"
        except ValueError:
            date_block = alert.starts_at

        title = alert.title or f"{alert.subject_name} anomaly"

        lines.extend(
            [
                f"• *{title}*",
                f"  - Namespace: `{alert.subject_namespace}` | Type: `{alert.subject_type or 'N/A'}`",
                f"  - Priority: `{alert.priority}` | Status: `{alert.status}`",
                f"  - Started at: {date_block}",
                "",
            ]
        )
