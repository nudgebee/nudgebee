"""MS Teams renderer for the weekly digest.

Adaptive Card equivalent of message_templates/slack/weekly_digest_events.py:
headline, scoreboard FactSet, the top findings, and an action back to the tab.
"""

from typing import Any, Dict, List

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.weekly_digest_events import (
    WeeklyDigestParams,
    finding_facts_line,
    finding_title,
    digest_headline,
    digest_link,
    scoreboard_pairs,
)


def get_teams_weekly_digest_events_template(params: WeeklyDigestParams) -> Dict[str, Any]:
    body: List[Dict[str, Any]] = [
        {
            "type": "TextBlock",
            "text": digest_headline(params),
            "size": "Large",
            "weight": "Bolder",
            "wrap": True,
        }
    ]

    subtitle = f"{settings.urls.branding_name} weekly digest"
    if params.period_label:
        subtitle += f" · {params.period_label}"
    body.append({"type": "TextBlock", "text": subtitle, "isSubtle": True, "wrap": True, "spacing": "None"})

    body.append(
        {
            "type": "FactSet",
            "facts": [{"title": label, "value": value} for label, value in scoreboard_pairs(params)],
            "separator": True,
        }
    )

    if params.lede:
        body.append({"type": "TextBlock", "text": params.lede, "wrap": True, "spacing": "Small"})

    for f in params.top_findings:
        body.append(
            {
                "type": "TextBlock",
                "text": finding_title(f),
                "weight": "Bolder",
                "wrap": True,
                "separator": True,
            }
        )
        if f.headline:
            body.append({"type": "TextBlock", "text": f.headline, "wrap": True, "spacing": "None"})
        facts = finding_facts_line(f)
        if facts:
            body.append({"type": "TextBlock", "text": facts, "isSubtle": True, "wrap": True, "spacing": "None"})

    if params.more_findings > 0:
        body.append(
            {
                "type": "TextBlock",
                "text": f"+{params.more_findings} more in the full review",
                "isSubtle": True,
                "wrap": True,
                "separator": True,
            }
        )

    return {
        "type": "AdaptiveCard",
        "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
        "version": "1.4",
        "body": body,
        "actions": [
            {
                "type": "Action.OpenUrl",
                "title": "View Full Review",
                "url": digest_link(params) or public_ip(),
            }
        ],
    }
