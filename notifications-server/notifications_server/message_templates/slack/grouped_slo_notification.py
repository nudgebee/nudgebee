from collections import defaultdict
from typing import List, Dict, Any
from datetime import datetime
from pydantic import BaseModel

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.slo import SLOAlertParams


class SLOAlertSummaryParams(BaseModel):
    events: List[SLOAlertParams]


def get_slo_aggregated_message_params(events: List[Dict[str, Any]]) -> SLOAlertSummaryParams:
    return SLOAlertSummaryParams(events=[SLOAlertParams(**e) for e in events])


def create_account_attachment(account_name: str, acct_alerts: List[SLOAlertParams]) -> Dict[str, Any]:
    """Create a collapsible attachment for account SLO alerts."""
    acct_id = acct_alerts[0].account_id

    lines = []
    for alert in acct_alerts:
        ts = int(float(alert.firing_since))
        # Slack date formatter; fallback to human format
        fallback = datetime.fromtimestamp(ts).strftime("%d %B %Y %I:%M %p")
        date_block = f"<!date^{ts}^{{date_short_pretty}} {{time}}|{fallback}>"

        lines.extend(
            [
                f"• Namespace: `{alert.namespace}` / Workload: `{alert.workload}`",
                f"• SLO: `{alert.slo_name}`",
                f"• Target: `{alert.slo_target}` / Current: `{alert.current_value}`",
                f"• Burn Rate: `{alert.burn_rate or 'N/A'}` / Budget Left: `{alert.error_budget_remaining or 'N/A'}`",
                f"• Firing Since: {date_block}",
                "",  # blank line between different alerts
            ]
        )

    blocks = [
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": f"*Account:* <{public_ip()}/kubernetes/details/{acct_id}|{account_name}>",
            },
        },
        {"type": "section", "text": {"type": "mrkdwn", "text": "\n".join(lines).strip()}},
    ]

    return {
        "color": "#e91e63",
        "fallback": f"{account_name}: {len(acct_alerts)} SLO breaches",
        "blocks": blocks,
    }


def get_grouped_slo_alerts_template(input_data: List[SLOAlertParams]) -> Dict[str, Any]:
    if isinstance(input_data, SLOAlertSummaryParams):
        alerts: List[SLOAlertParams] = input_data.events
    else:
        alerts = input_data

    grouped: Dict[str, List[SLOAlertParams]] = defaultdict(list)
    for alert in alerts:
        grouped[alert.account_name].append(alert)

    # Header blocks
    blocks: List[Dict[str, Any]] = [
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": f"*{len(alerts)} SLO Breach Alerts across {len(grouped)} accounts*",
            },
        },
        {"type": "divider"},
    ]

    # Create collapsible attachments for each account
    attachments = []
    for account_name, acct_alerts in grouped.items():
        attachments.append(create_account_attachment(account_name, acct_alerts))

    # Footer
    blocks.append(
        {
            "type": "context",
            "elements": [{"type": "mrkdwn", "text": "Review impacted workloads to restore SLO compliance."}],
        }
    )

    return {
        "text": "Grouped SLO Alert Summary",
        "blocks": blocks,
        "attachments": attachments[:20],  # Slack limit
        "unfurl_links": False,
    }
