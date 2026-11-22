from collections import defaultdict
from typing import List, Dict, Any
from datetime import datetime
from pydantic import BaseModel

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.google_chat.grouped_anomaly_notification import (
    MAX_TEXT_LENGTH,
    truncate_text,
)
from notifications_server.message_templates.slack.slo import SLOAlertParams
from notifications_server.message_templates.slack.grouped_anomaly_notification import (
    MAX_RESOLVED_ITEMS,
    counts_phrase,
    split_resolved,
)
from notifications_server.message_templates.slack.grouped_slo_notification import resolved_slo_line
from notifications_server.message_templates.base import Emojis, format_burn_rate


class SLOAlertSummaryParams(BaseModel):
    events: List[SLOAlertParams]


def get_grouped_slo_alerts_gchat_template(input_data: List[SLOAlertParams]) -> Dict[str, Any]:
    lines: List[str] = []
    if not input_data:
        return {"text": "✅ No SLO Breach Alerts to report"}

    if not hasattr(input_data, "events"):
        raise TypeError("Expected SLOAlertSummaryParams with an events list")
    alerts: List[SLOAlertParams] = input_data.events

    firing, resolved = split_resolved(alerts)
    grouped: Dict[str, List[SLOAlertParams]] = defaultdict(list)
    for alert in firing:
        grouped[alert.account_name].append(alert)

    resolved_only = bool(resolved) and not firing
    emoji = Emojis.Resolved.value if resolved_only else Emojis.Alert.value
    counts = counts_phrase(len(firing), len(resolved), "SLO Breach Alerts", "")
    total_accounts = len({alert.account_name for alert in alerts})

    lines.append(f"{emoji} {counts} across {total_accounts} accounts")
    lines.append("")

    for account_name, acct_alerts in grouped.items():
        acct_id = acct_alerts[0].account_id
        url = f"{public_ip()}/kubernetes/details/{acct_id}"
        lines.append(f"*Account:* <{url}|{account_name}>")
        for alert in acct_alerts:
            ts = int(float(alert.firing_since))
            human = datetime.fromtimestamp(ts).strftime("%d %b %Y %I:%M %p")

            lines.append(f"• Namespace: {alert.namespace} / Workload: {alert.workload}")
            lines.append(f"• SLO: {alert.slo_name}")
            lines.append(f"• Target: {alert.slo_target} / Current: {alert.current_value}")
            lines.append(
                f"• Burn Rate: {format_burn_rate(alert.burn_rate, alert.burn_rate_window) or 'N/A'} / Budget Left:"
                f" {alert.error_budget_remaining or 'N/A'}"
            )
            lines.append(f"• Firing Since: {human}")
            lines.append("")

    if not resolved_only:
        lines.append(f"{Emojis.Event.value} Review impacted workloads to restore SLO compliance.")

    message = "\n".join(lines)
    if not resolved:
        return {"text": message}

    # The section is short and bounded (capped items, clipped lines), so it is
    # held back whole and only the firing part above it is ever truncated. A
    # digest without the section is sent at full length, as it always was.
    section = [f"{Emojis.Resolved.value} *Resolved*"]
    section += [f"• {resolved_slo_line(alert, '*')}" for alert in resolved[:MAX_RESOLVED_ITEMS]]
    if len(resolved) > MAX_RESOLVED_ITEMS:
        section.append(f"+{len(resolved) - MAX_RESOLVED_ITEMS} more resolved")
    tail = "\n\n" + "\n".join(section)
    return {"text": truncate_text(message.rstrip("\n"), MAX_TEXT_LENGTH - len(tail)) + tail}
