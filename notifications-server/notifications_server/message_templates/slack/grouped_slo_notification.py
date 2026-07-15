from typing import List, Dict, Any, Union
from datetime import datetime
from pydantic import BaseModel

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_CRITICAL,
    item_attachment,
    neutral_footer_attachment,
)
from notifications_server.message_templates.slack.slo import SLOAlertParams

MAX_SLO_ITEMS = 5


class SLOAlertSummaryParams(BaseModel):
    events: List[SLOAlertParams]


def get_slo_aggregated_message_params(events: List[Dict[str, Any]]) -> SLOAlertSummaryParams:
    return SLOAlertSummaryParams(events=[SLOAlertParams(**e) for e in events])


def _firing_since_block(firing_since: Union[str, float, int]) -> str:
    try:
        ts = int(float(firing_since))
        fallback = datetime.fromtimestamp(ts).strftime("%d %B %Y %I:%M %p")
    except (TypeError, ValueError, OSError, OverflowError):
        return str(firing_since)
    return f"<!date^{ts}^{{date_short_pretty}} {{time}}|{fallback}>"


# Fast-burn threshold: at >=10x the budget drains within hours -> BREACHING;
# slower burns read DEGRADING (matches the design mocks' badge split).
FAST_BURN = 10


def _slo_badge(alert: SLOAlertParams) -> str:
    try:
        return "BREACHING" if float(alert.burn_rate) >= FAST_BURN else "DEGRADING"
    except (TypeError, ValueError):
        return "BREACHING"


def _burn_line(alert: SLOAlertParams) -> str:
    parts = []
    if alert.burn_rate not in (None, "", "N/A"):
        parts.append(f"burning budget *{alert.burn_rate}× too fast*")
    if alert.error_budget_remaining not in (None, "", "N/A"):
        parts.append(f"*{alert.error_budget_remaining}* of budget left")
    return " — ".join(parts)


def get_grouped_slo_alerts_template(input_data: List[SLOAlertParams]) -> Dict[str, Any]:
    if isinstance(input_data, SLOAlertSummaryParams):
        alerts: List[SLOAlertParams] = input_data.events
    else:
        alerts = input_data

    count = len(alerts)
    headline = "1 SLO is burning error budget" if count == 1 else f"{count} SLOs are burning error budget"

    blocks: List[Dict[str, Any]] = [
        {"type": "section", "text": {"type": "mrkdwn", "text": f"*{headline}*"}},
        {"type": "divider"},
    ]

    attachments = []
    for alert in alerts[:MAX_SLO_ITEMS]:
        lines = [
            f"*{alert.slo_name}* `{_slo_badge(alert)}`",
            f"At *{alert.current_value}* against a *{alert.slo_target}* target",
        ]
        burn = _burn_line(alert)
        if burn:
            lines.append(burn[0].upper() + burn[1:])

        account_link = f"<{public_ip()}/kubernetes/details/{alert.account_id}|{alert.account_name}>"
        identity = (
            f"{alert.namespace}/{alert.workload} · firing since {_firing_since_block(alert.firing_since)} · "
            f"Acct {account_link}"
        )
        item_blocks = [
            {"type": "section", "text": {"type": "mrkdwn", "text": "\n".join(lines)}},
            {"type": "context", "elements": [{"type": "mrkdwn", "text": identity}]},
        ]
        attachments.append(item_attachment(STRIPE_CRITICAL, alert.slo_name, item_blocks))

    footer_blocks = []
    remaining = count - MAX_SLO_ITEMS
    if remaining > 0:
        footer_blocks.append(
            {"type": "section", "text": {"type": "mrkdwn", "text": f"_+{remaining} more SLOs breaching_"}}
        )
    footer_blocks.append(
        {
            "type": "context",
            "elements": [{"type": "mrkdwn", "text": "Review impacted workloads to restore SLO compliance."}],
        }
    )
    attachments.append(neutral_footer_attachment(footer_blocks, "SLO summary"))

    return {
        "text": headline,
        "blocks": blocks[:50],
        "attachments": attachments[:20],
        "unfurl_links": False,
    }
