from typing import List, Dict, Any, Optional, Union
from datetime import datetime, timezone
from pydantic import BaseModel

from notifications_server.configs.settings import URLRoutes, settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_CRITICAL,
    header_block,
    legacy_attachment,
    link_button,
    neutral_footer_attachment,
)
from notifications_server.message_templates.slack.slo import SLOAlertParams

MAX_SLO_ITEMS = 5

# Guard against producers sending ms/us epochs (would render far-future dates).
MAX_REASONABLE_EPOCH = 4102444800  # 2100-01-01


class SLOAlertSummaryParams(BaseModel):
    events: List[SLOAlertParams]


def get_slo_aggregated_message_params(events: List[Dict[str, Any]]) -> SLOAlertSummaryParams:
    return SLOAlertSummaryParams(events=[SLOAlertParams(**e) for e in events])


def _epoch(value: Union[str, float, int]) -> Optional[int]:
    try:
        ts = int(float(value))
    except (TypeError, ValueError):
        return None
    if 0 < ts < MAX_REASONABLE_EPOCH:
        return ts
    return None


def _firing_since(firing_since: Union[str, float, int]) -> str:
    """Inline localized time via Slack's <!date> token; raw value when the
    producer sends something unparseable."""
    ts = _epoch(firing_since)
    if ts is None:
        return f"firing since {firing_since}"
    try:
        fallback = datetime.fromtimestamp(ts, tz=timezone.utc).strftime("%d %b %Y %I:%M %p UTC")
    except (ValueError, OSError, OverflowError):
        return f"firing since {firing_since}"
    return f"firing since <!date^{ts}^{{date_short_pretty}} {{time}}|{fallback}>"


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

    blocks: List[Dict[str, Any]] = [header_block(headline), {"type": "divider"}]

    attachments = []
    for alert in alerts[:MAX_SLO_ITEMS]:
        lines = [
            f"*{alert.slo_name}* `{_slo_badge(alert)}`",
            f"At *{alert.current_value}* against a *{alert.slo_target}* target",
        ]
        burn = _burn_line(alert)
        if burn:
            lines.append(burn[0].upper() + burn[1:])
        lines.append(
            f"{alert.namespace}/{alert.workload} · Acct: {alert.account_name} · {_firing_since(alert.firing_since)}"
        )

        attachments.append(
            legacy_attachment(
                STRIPE_CRITICAL,
                alert.slo_name,
                text="\n".join(lines),
                actions=[
                    link_button(
                        "Details",
                        settings.urls.cluster_details_url(
                            alert.account_id,
                            utm_source=URLRoutes.UTMSource.SLACK,
                            anchor=URLRoutes.Anchors.MONITORING_SLO,
                        ),
                        style="primary",
                    )
                ],
            )
        )

    remaining = count - MAX_SLO_ITEMS
    if remaining > 0:
        attachments.append(
            neutral_footer_attachment(text=f"_+{remaining} more SLOs breaching_", fallback="SLO summary")
        )

    return {
        "text": headline,
        "blocks": blocks[:50],
        "attachments": attachments[:20],
        "unfurl_links": False,
    }
