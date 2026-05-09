from collections import defaultdict
from typing import List, Dict, Any
from datetime import datetime

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.base import Emojis
from notifications_server.message_templates.slack.grouped_anomaly_notification import AnomalyAlertParams

MAX_TEXT_LENGTH = 3800


def truncate_text(text: str, max_len: int = MAX_TEXT_LENGTH) -> str:
    return text if len(text) <= max_len else text[: max_len - 3] + "..."


def get_grouped_anomaly_alerts_gchat_template(input_data: Any) -> Dict[str, Any]:
    lines: List[str] = []

    # Support both raw list and wrapper
    if not input_data:
        return {"text": "✅ No Anomaly Alerts to report"}

    alerts: List[AnomalyAlertParams]
    if hasattr(input_data, "events"):
        alerts = input_data.events
    elif isinstance(input_data, list):
        alerts = input_data
    else:
        raise TypeError("Expected AnomalyAlertSummaryParams or list[AnomalyAlertParams]")

    grouped: Dict[str, List[AnomalyAlertParams]] = defaultdict(list)
    for alert in alerts:
        grouped[alert.cloud_account_id].append(alert)

    total_alerts = len(alerts)
    total_accounts = len(grouped)

    lines.append(f"{Emojis.Alert.value} *{total_alerts} Anomalies detected across {total_accounts} accounts*\n")

    for cloud_account_id, acct_alerts in grouped.items():
        cluster_name = acct_alerts[0].cluster or "Cluster"
        url = f"{public_ip()}/kubernetes/details/{cloud_account_id}?tab=2&subtab=6#events/anomaly"
        lines.append(f"*Account:* <{url}|{cluster_name}>")

        for alert in acct_alerts:
            try:
                ts = int(datetime.fromisoformat(alert.starts_at.replace("Z", "")).timestamp())
                human = datetime.fromtimestamp(ts).strftime("%d %b %Y %I:%M %p")
            except ValueError:
                human = alert.starts_at

            title = alert.title or f"{alert.subject_name} anomaly"

            lines.append(f"• *{title}*")
            lines.append(f"  - Namespace: *{alert.subject_namespace}* | Type: *{alert.subject_type or 'N/A'}*")
            lines.append(f"  - Priority: *{alert.priority}* | Status: *{alert.status}*")
            lines.append(f"  - Started at: {human}")
            lines.append("")

    lines.append(f"{Emojis.Event.value} Review detected anomalies and investigate impacted workloads in your clusters.")

    message = "\n".join(lines)
    return {"text": truncate_text(message)}
