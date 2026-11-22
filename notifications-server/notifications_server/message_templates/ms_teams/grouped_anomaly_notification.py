import logging
from collections import defaultdict
from typing import List, Dict, Any, Optional
from datetime import datetime

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.base import Emojis
from notifications_server.message_templates.slack.grouped_anomaly_notification import (
    MAX_ANOMALY_ITEMS,
    MAX_RESOLVED_ITEMS,
    AnomalyAlertParams,
    counts_phrase,
    resolved_line,
    returned_label,
    split_resolved,
)

LOG = logging.getLogger(__name__)

MAX_CARD_TEXT_LENGTH = 3800


def truncate_text(text: str, max_len: int = MAX_CARD_TEXT_LENGTH) -> str:
    return text if len(text) <= max_len else text[: max_len - 3] + "..."


def get_grouped_anomaly_alerts_ms_teams_template(input_data: Any) -> Dict[str, Any]:
    if not input_data:
        return {
            "type": "AdaptiveCard",
            "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
            "version": "1.0",
            "body": [{"type": "TextBlock", "text": "No Anomaly Alerts to report", "wrap": True}],
        }

    if hasattr(input_data, "events"):
        alerts: List[AnomalyAlertParams] = input_data.events
    elif isinstance(input_data, list):
        alerts = input_data
    else:
        raise TypeError("Expected AnomalyAlertSummaryParams or list[AnomalyAlertParams]")

    firing, resolved = split_resolved(alerts)
    grouped: Dict[str, List[AnomalyAlertParams]] = defaultdict(list)
    for alert in firing:
        grouped[alert.cloud_account_id].append(alert)

    resolved_only = bool(resolved) and not firing
    emoji = Emojis.Resolved.value if resolved_only else Emojis.Alert.value
    counts = counts_phrase(len(firing), len(resolved), "Anomalies")
    total_accounts = len({alert.cloud_account_id for alert in alerts})
    header: List[Dict[str, Any]] = [
        {
            "type": "TextBlock",
            "text": f"{emoji} **{counts} across {total_accounts} accounts**",
            "size": "Large",
            "weight": "Bolder",
            "wrap": True,
        },
        {"type": "TextBlock", "text": " ", "separator": True},
    ]

    # Built per account, not appended straight into one body list: if the full
    # card still doesn't fit Teams' size limit (many accounts, not just many
    # anomalies in one), whole accounts are dropped from the tail instead of
    # truncating text mid-alert.
    account_sections: List[List[Dict[str, Any]]] = []
    for cloud_account_id, acct_alerts in grouped.items():
        cluster_name = acct_alerts[0].cluster or "Cluster"
        account_url = f"{public_ip()}/kubernetes/details/{cloud_account_id}?tab=2&subtab=6#events/anomaly"

        section: List[Dict[str, Any]] = [
            {
                "type": "TextBlock",
                "text": f"**Account:** [{cluster_name}]({account_url})",
                "weight": "Bolder",
                "wrap": True,
                "spacing": "Medium",
            }
        ]

        for alert in acct_alerts[:MAX_ANOMALY_ITEMS]:
            try:
                ts = int(datetime.fromisoformat(alert.starts_at.replace("Z", "")).timestamp())
                human = datetime.fromtimestamp(ts).strftime("%d %b %Y %I:%M %p")
            except ValueError:
                human = alert.starts_at

            title = alert.title or f"{alert.subject_name} anomaly"

            section.extend(
                [
                    {
                        "type": "TextBlock",
                        "text": f"- **{title}**{returned_label(alert, '**')}",
                        "wrap": True,
                    },
                    {
                        "type": "TextBlock",
                        "text": f"  Namespace: *{alert.subject_namespace}* | Type: *{alert.subject_type or 'N/A'}*",
                        "wrap": True,
                    },
                    {
                        "type": "TextBlock",
                        "text": f"  Priority: *{alert.priority}* | Status: *{alert.status}*",
                        "wrap": True,
                    },
                    {
                        "type": "TextBlock",
                        "text": f"  Started at: {human}",
                        "wrap": True,
                    },
                ]
            )
        if len(acct_alerts) > MAX_ANOMALY_ITEMS:
            section.append({"type": "TextBlock", "text": f"+{len(acct_alerts) - MAX_ANOMALY_ITEMS} more", "wrap": True})
        section.append({"type": "TextBlock", "text": " ", "separator": True})
        account_sections.append(section)

    tail: List[Dict[str, Any]] = []
    if not resolved_only:
        tail.append(
            {
                "type": "TextBlock",
                "text": (
                    f"{Emojis.Event.value} Review detected anomalies and investigate impacted workloads "
                    "in your clusters."
                ),
                "wrap": True,
                "spacing": "Medium",
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
            tail.append({"type": "TextBlock", "text": f"- {resolved_line(alert, '**')}", "wrap": True})
        if len(resolved) > MAX_RESOLVED_ITEMS:
            tail.append(
                {"type": "TextBlock", "text": f"+{len(resolved) - MAX_RESOLVED_ITEMS} more resolved", "wrap": True}
            )

    return _build_card(header, account_sections, tail, "anomalies")


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
            "version": "1.4",
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
