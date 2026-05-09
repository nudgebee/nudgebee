from collections import defaultdict
from typing import List, Dict, Any
from datetime import datetime
from pydantic import BaseModel

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.slo import SLOAlertParams
from notifications_server.message_templates.base import Emojis


class SLOAlertSummaryParams(BaseModel):
    events: List[SLOAlertParams]


def get_grouped_slo_alerts_ms_teams_template(input_data: List[SLOAlertParams]) -> Dict[str, Any]:
    if not hasattr(input_data, "events"):
        raise TypeError("Expected SLOAlertSummaryParams with an events list")
    alerts: List[SLOAlertParams] = input_data.events

    grouped: Dict[str, List[SLOAlertParams]] = defaultdict(list)
    for alert in alerts:
        grouped[alert.account_name].append(alert)

    body: List[Dict[str, Any]] = [
        {
            "type": "TextBlock",
            "text": f"{Emojis.Alert.value} **{len(alerts)} SLO Breach Alerts across {len(grouped)} accounts**",
            "size": "Large",
            "weight": "Bolder",
            "wrap": True,
        },
        {"type": "TextBlock", "text": "---", "separator": True},
    ]

    for account_name, acct_alerts in grouped.items():
        acct_id = acct_alerts[0].account_id
        body.append(
            {
                "type": "TextBlock",
                "text": f"**Account:** [{account_name}]({public_ip()}/kubernetes/details/{acct_id})",
                "weight": "Bolder",
                "wrap": True,
            }
        )

        for alert in acct_alerts:
            ts = int(float(alert.firing_since))
            human = datetime.fromtimestamp(ts).strftime("%d %b %Y %I:%M %p")
            # each line its own block so Teams renders line-by-line
            body.append(
                {
                    "type": "TextBlock",
                    "text": f"• Namespace: `{alert.namespace}` / Workload: `{alert.workload}`",
                    "wrap": True,
                }
            )
            body.append({"type": "TextBlock", "text": f"• SLO: `{alert.slo_name}`", "wrap": True})
            body.append(
                {
                    "type": "TextBlock",
                    "text": f"• Target: `{alert.slo_target}` / Current: `{alert.current_value}`",
                    "wrap": True,
                }
            )
            body.append(
                {
                    "type": "TextBlock",
                    "text": (
                        f"• Burn Rate: `{alert.burn_rate or 'N/A'}` / Budget Left:"
                        f" `{alert.error_budget_remaining or 'N/A'}`"
                    ),
                    "wrap": True,
                }
            )
            body.append({"type": "TextBlock", "text": f"• Firing Since: {human}", "wrap": True})
        body.append({"type": "TextBlock", "text": "", "separator": True})

        # Footer
    body.append(
        {
            "type": "TextBlock",
            "text": f"{Emojis.Event.value} Review impacted workloads to restore SLO compliance.",
            "wrap": True,
        }
    )

    card_payload: Dict[str, Any] = {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.0",
        "msteams": {"width": "Full"},
        "body": body,
    }

    return card_payload
