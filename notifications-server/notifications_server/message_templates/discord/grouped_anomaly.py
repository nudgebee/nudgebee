"""Discord renderer for grouped anomaly alerts.

Mirrors message_templates/slack/grouped_anomaly_notification.py — anomalies are
grouped by cloud account and rendered one embed per account (Discord caps at 10
embeds/message and 4096 chars/description).
"""

from collections import defaultdict
from typing import Any, Dict, List

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.grouped_anomaly_notification import (
    AnomalyAlertParams,
    AnomalyAlertSummaryParams,
)

ANOMALY_COLOR = 16750848  # orange (#ff9800)
MAX_ALERTS_PER_ACCOUNT = 8
MAX_EMBEDS = 10


def _v(value: Any) -> str:
    if value is None or value == "":
        return "—"
    return str(value)


def get_discord_grouped_anomaly_template(input_data: AnomalyAlertSummaryParams) -> Dict[str, Any]:
    alerts: List[AnomalyAlertParams] = input_data.events if hasattr(input_data, "events") else input_data

    grouped: Dict[str, List[AnomalyAlertParams]] = defaultdict(list)
    for alert in alerts:
        grouped[alert.cloud_account_id].append(alert)

    embeds: List[Dict[str, Any]] = []
    for cloud_account_id, acct_alerts in grouped.items():
        if len(embeds) >= MAX_EMBEDS:
            break
        cluster_name = acct_alerts[0].cluster or "Cluster"
        account_url = f"{public_ip()}/kubernetes/details/{cloud_account_id}?tab=2&subtab=6#events/anomaly"
        lines = [
            f"• **{_v(a.title or f'{a.subject_name} anomaly')}** — {_v(a.priority)}/{_v(a.status)} "
            f"on `{_v(a.subject_namespace)}/{_v(a.subject_name)}`"
            for a in acct_alerts[:MAX_ALERTS_PER_ACCOUNT]
        ]
        if len(acct_alerts) > MAX_ALERTS_PER_ACCOUNT:
            lines.append(f"…and {len(acct_alerts) - MAX_ALERTS_PER_ACCOUNT} more anomalies in this account.")
        embeds.append(
            {
                "title": f"Account: {cluster_name}",
                "url": account_url,
                "description": "\n".join(lines)[:4000],
                "color": ANOMALY_COLOR,
            }
        )

    content = f"⚠️ {len(alerts)} anomalies detected across {len(grouped)} account(s)"
    return {"content": content, "embeds": embeds}
