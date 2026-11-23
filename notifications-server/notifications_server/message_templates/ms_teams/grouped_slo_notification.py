import logging
from collections import defaultdict
from typing import List, Dict, Any
from datetime import datetime
from pydantic import BaseModel

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.slo import SLOAlertParams
from notifications_server.message_templates.slack.grouped_anomaly_notification import (
    MAX_RESOLVED_ITEMS,
    counts_phrase,
    split_resolved,
)
from notifications_server.message_templates.slack.grouped_slo_notification import MAX_SLO_ITEMS, resolved_slo_line
from notifications_server.message_templates.base import Emojis, format_burn_rate

LOG = logging.getLogger(__name__)


class SLOAlertSummaryParams(BaseModel):
    events: List[SLOAlertParams]


def get_grouped_slo_alerts_ms_teams_template(input_data: List[SLOAlertParams]) -> Dict[str, Any]:
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
    header: List[Dict[str, Any]] = [
        {
            "type": "TextBlock",
            "text": f"{emoji} **{counts} across {total_accounts} accounts**",
            "size": "Large",
            "weight": "Bolder",
            "wrap": True,
        },
        {"type": "TextBlock", "text": "---", "separator": True},
    ]

    # Built per account, not appended straight into one body list: if the full
    # card still doesn't fit Teams' size limit (many accounts, not just many
    # breaches in one), whole accounts are dropped from the tail instead of
    # truncating text mid-alert.
    account_sections: List[List[Dict[str, Any]]] = []
    for account_name, acct_alerts in grouped.items():
        acct_id = acct_alerts[0].account_id
        section: List[Dict[str, Any]] = [
            {
                "type": "TextBlock",
                "text": f"**Account:** [{account_name}]({public_ip()}/kubernetes/details/{acct_id})",
                "weight": "Bolder",
                "wrap": True,
            }
        ]

        for alert in acct_alerts[:MAX_SLO_ITEMS]:
            ts = int(float(alert.firing_since))
            human = datetime.fromtimestamp(ts).strftime("%d %b %Y %I:%M %p")
            # each line its own block so Teams renders line-by-line
            section.append(
                {
                    "type": "TextBlock",
                    "text": f"• Namespace: `{alert.namespace}` / Workload: `{alert.workload}`",
                    "wrap": True,
                }
            )
            section.append({"type": "TextBlock", "text": f"• SLO: `{alert.slo_name}`", "wrap": True})
            section.append(
                {
                    "type": "TextBlock",
                    "text": f"• Target: `{alert.slo_target}` / Current: `{alert.current_value}`",
                    "wrap": True,
                }
            )
            section.append(
                {
                    "type": "TextBlock",
                    "text": (
                        f"• Burn Rate: `{format_burn_rate(alert.burn_rate, alert.burn_rate_window) or 'N/A'}` / Budget"
                        f" Left: `{alert.error_budget_remaining or 'N/A'}`"
                    ),
                    "wrap": True,
                }
            )
            section.append({"type": "TextBlock", "text": f"• Firing Since: {human}", "wrap": True})
        if len(acct_alerts) > MAX_SLO_ITEMS:
            section.append({"type": "TextBlock", "text": f"+{len(acct_alerts) - MAX_SLO_ITEMS} more", "wrap": True})
        section.append({"type": "TextBlock", "text": "", "separator": True})
        account_sections.append(section)

    tail: List[Dict[str, Any]] = []
    if not resolved_only:
        tail.append(
            {
                "type": "TextBlock",
                "text": f"{Emojis.Event.value} Review impacted workloads to restore SLO compliance.",
                "wrap": True,
            }
        )

    if resolved:
        tail.append(
            {
                "type": "TextBlock",
                "text": f"{Emojis.Resolved.value} **Resolved**",
                "weight": "Bolder",
                "wrap": True,
                "spacing": "Medium",
            }
        )
        for alert in resolved[:MAX_RESOLVED_ITEMS]:
            tail.append({"type": "TextBlock", "text": f"- {resolved_slo_line(alert, '**')}", "wrap": True})
        if len(resolved) > MAX_RESOLVED_ITEMS:
            tail.append(
                {"type": "TextBlock", "text": f"+{len(resolved) - MAX_RESOLVED_ITEMS} more resolved", "wrap": True}
            )

    return _build_card(header, account_sections, tail, "SLO breaches")


def _build_card(
    header: List[Dict[str, Any]],
    account_sections: List[List[Dict[str, Any]]],
    tail: List[Dict[str, Any]],
    noun: str,
) -> Dict[str, Any]:
    # Imported here, not at module level: message_templates/__init__.py pulls this
    # module in during notifications_server/__init__.py's own bootstrap, before
    # sync_engine exists — card_actions needs it at import time.
    from notifications_server.services.ms_teams import card_actions

    def card(shown: int) -> Dict[str, Any]:
        body = list(header)
        for section in account_sections[:shown]:
            body.extend(section)
        dropped = len(account_sections) - shown
        if dropped:
            body.append({"type": "TextBlock", "text": f"+{dropped} more accounts with {noun}", "wrap": True})
        body.extend(tail)
        return {
            "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
            "type": "AdaptiveCard",
            "version": "1.0",
            "msteams": {"width": "Full"},
            "body": body,
        }

    shown = len(account_sections)
    result = card(shown)
    while shown > 0 and not card_actions.fits(result):
        shown -= 1
        result = card(shown)
    if not card_actions.fits(result):
        LOG.warning("Teams grouped %s card still too large after dropping every account", noun)
    return result
