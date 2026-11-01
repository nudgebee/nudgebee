"""Discord renderer for grouped anomaly alerts.

Mirrors message_templates/slack/grouped_anomaly_notification.py — anomalies are
grouped by cloud account and rendered one embed per account (Discord caps at 10
embeds/message and 4096 chars/description).
"""

from collections import defaultdict
from typing import Any, Dict, List

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.grouped_anomaly_notification import (
    MAX_RESOLVED_ITEMS,
    AnomalyAlertParams,
    AnomalyAlertSummaryParams,
    counts_phrase,
    resolved_line,
    returned_label,
    split_resolved,
)

ANOMALY_COLOR = 16750848  # orange (#ff9800)
RESOLVED_COLOR = 3066993  # green (#2ecc71)
MAX_ALERTS_PER_ACCOUNT = 8
MAX_EMBEDS = 10


def _v(value: Any) -> str:
    if value is None or value == "":
        return "—"
    return str(value)


def get_discord_grouped_anomaly_template(input_data: AnomalyAlertSummaryParams) -> Dict[str, Any]:
    alerts: List[AnomalyAlertParams] = input_data.events if hasattr(input_data, "events") else input_data

    firing, resolved = split_resolved(alerts)
    grouped: Dict[str, List[AnomalyAlertParams]] = defaultdict(list)
    for alert in firing:
        grouped[alert.cloud_account_id].append(alert)

    # The Resolved embed takes one of the message's embed slots.
    max_account_embeds = MAX_EMBEDS - 1 if resolved else MAX_EMBEDS
    embeds: List[Dict[str, Any]] = []
    for cloud_account_id, acct_alerts in grouped.items():
        if len(embeds) >= max_account_embeds:
            break
        cluster_name = acct_alerts[0].cluster or "Cluster"
        account_url = f"{public_ip()}/kubernetes/details/{cloud_account_id}?tab=2&subtab=6#events/anomaly"
        lines = [
            f"• **{_v(a.title or f'{a.subject_name} anomaly')}**{returned_label(a, '**')}"
            f" — {_v(a.priority)}/{_v(a.status)} on `{_v(a.subject_namespace)}/{_v(a.subject_name)}`"
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

    if resolved:
        lines = [f"• {resolved_line(a, '**')}" for a in resolved[:MAX_RESOLVED_ITEMS]]
        if len(resolved) > MAX_RESOLVED_ITEMS:
            lines.append(f"…and {len(resolved) - MAX_RESOLVED_ITEMS} more resolved.")
        embeds.append({"title": "Resolved", "description": "\n".join(lines)[:4000], "color": RESOLVED_COLOR})

    emoji = "✅" if resolved and not firing else "⚠️"
    counts = counts_phrase(len(firing), len(resolved), "anomalies")
    total_accounts = len({alert.cloud_account_id for alert in alerts})
    content = f"{emoji} {counts} across {total_accounts} account(s)"
    # The Resolved embed can push out an account that would otherwise be shown,
    # so say how many accounts have no embed.
    hidden_accounts = len(grouped) - max_account_embeds
    if resolved and hidden_accounts > 0:
        content += f", {hidden_accounts} account(s) not shown"
    return {"content": content, "embeds": embeds}
