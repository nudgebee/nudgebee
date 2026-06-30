"""Discord renderers for SLO-alert notifications (single + grouped).

Mirrors the data surfaced by the Slack SLO templates (message_templates/slack/
slo.py and grouped_slo_notification.py) as Discord embeds. Returns the
{"content", "embeds"} payload consumed by DiscordClient.chat_post.
"""

import logging
from collections import defaultdict
from datetime import datetime, timezone
from typing import Any, Dict, List

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.slo import SLOAlertParams
from notifications_server.message_templates.slack.grouped_slo_notification import SLOAlertSummaryParams

LOG = logging.getLogger(__name__)

# Discord embed color (red) for SLO breaches; Discord caps embeds at 25 fields
# and 10 embeds/message, so grouped output is chunked and capped.
SLO_ALERT_COLOR = 15158332
MAX_ALERTS_PER_ACCOUNT = 8
MAX_EMBEDS = 10


def _v(value: Any) -> str:
    """Discord rejects empty field values; render None/empty as an em dash."""
    if value is None or value == "":
        return "—"
    return str(value)


def _account_url(account_id: str) -> str:
    return f"{public_ip()}/kubernetes/details/{account_id}"


def get_discord_slo_alert_template(params: SLOAlertParams) -> Dict[str, Any]:
    description = (
        f"**Account:** [{_v(params.account_name)}]({_account_url(params.account_id)})\n"
        f"**Namespace:** `{_v(params.namespace)}`\n"
        f"**Workload:** `{_v(params.workload)}`"
    )
    embed: Dict[str, Any] = {
        "title": f"SLO Alert: {_v(params.slo_name)}",
        "description": description,
        "color": SLO_ALERT_COLOR,
        "fields": [
            {"name": "Status", "value": _v(params.status), "inline": True},
            {"name": "Target", "value": _v(params.slo_target), "inline": True},
            {"name": "Current Value", "value": _v(params.current_value), "inline": True},
            {"name": "Burn Rate", "value": _v(params.burn_rate), "inline": True},
            {"name": "Budget Remaining", "value": _v(params.error_budget_remaining), "inline": True},
            {"name": "Threshold", "value": _v(params.threshold), "inline": True},
            {"name": "Good Events", "value": _v(params.good_event_count), "inline": True},
            {"name": "Bad Events", "value": _v(params.bad_event_count), "inline": True},
        ],
    }
    try:
        embed["timestamp"] = datetime.fromtimestamp(float(params.firing_since), tz=timezone.utc).isoformat()
    except Exception as exc:  # pragma: no cover - defensive
        LOG.debug("Unable to parse SLO firing_since for Discord timestamp: %s", exc)

    return {"content": f"🚨 SLO Alert: {_v(params.slo_name)}", "embeds": [embed]}


def get_discord_grouped_slo_alert_template(input_data: SLOAlertSummaryParams) -> Dict[str, Any]:
    alerts: List[SLOAlertParams] = input_data.events if hasattr(input_data, "events") else input_data

    grouped: Dict[str, List[SLOAlertParams]] = defaultdict(list)
    for alert in alerts:
        grouped[alert.account_name or alert.account_id].append(alert)

    embeds: List[Dict[str, Any]] = []
    for account_name, acct_alerts in grouped.items():
        if len(embeds) >= MAX_EMBEDS:
            break
        lines = [
            f"• **{_v(a.slo_name)}** (`{_v(a.workload)}`) — {_v(a.status)}, "
            f"budget remaining {_v(a.error_budget_remaining)}"
            for a in acct_alerts[:MAX_ALERTS_PER_ACCOUNT]
        ]
        if len(acct_alerts) > MAX_ALERTS_PER_ACCOUNT:
            lines.append(f"…and {len(acct_alerts) - MAX_ALERTS_PER_ACCOUNT} more in this account.")
        embeds.append(
            {
                "title": f"Account: {account_name}",
                "url": _account_url(acct_alerts[0].account_id),
                "description": "\n".join(lines)[:4000],
                "color": SLO_ALERT_COLOR,
            }
        )

    content = f"🚨 {len(alerts)} SLO alerts across {len(grouped)} account(s)"
    return {"content": content, "embeds": embeds}
