from collections import defaultdict
from typing import List, Dict, Any
from datetime import datetime
from pydantic import BaseModel

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.slo import SLOAlertParams
from notifications_server.message_templates.base import Emojis


class SLOAlertSummaryParams(BaseModel):
    events: List[SLOAlertParams]


def get_grouped_slo_alerts_gchat_template(input_data: List[SLOAlertParams]) -> Dict[str, Any]:
    lines: List[str] = []
    if not input_data:
        return {"text": "✅ No SLO Breach Alerts to report"}

    if not hasattr(input_data, "events"):
        raise TypeError("Expected SLOAlertSummaryParams with an events list")
    alerts: List[SLOAlertParams] = input_data.events

    grouped: Dict[str, List[SLOAlertParams]] = defaultdict(list)
    for alert in alerts:
        grouped[alert.account_name].append(alert)

    lines.append(f"{Emojis.Alert.value} {len(alerts)} SLO Breach Alerts across {len(grouped)} accounts")
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
                f"• Burn Rate: {alert.burn_rate or 'N/A'} / Budget Left: {alert.error_budget_remaining or 'N/A'}"
            )
            lines.append(f"• Firing Since: {human}")
            lines.append("")

    lines.append(f"{Emojis.Event.value} Review impacted workloads to restore SLO compliance.")

    return {"text": "\n".join(lines)}
