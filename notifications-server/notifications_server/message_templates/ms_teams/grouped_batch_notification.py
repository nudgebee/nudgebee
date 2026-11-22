from typing import Any, Dict

from notifications_server.message_templates.slack.grouped_batch_notification import (
    BatchFindingsSummaryParams,
    item_details_url,
    view_all_url,
)

UTM_SOURCE = "teams"


def get_grouped_batch_findings_ms_teams_template(input_data: BatchFindingsSummaryParams) -> Dict[str, Any]:
    noun = "finding" if input_data.total_count == 1 else "findings"
    body = [
        {
            "type": "TextBlock",
            "text": f"{input_data.total_count} {noun} held for this hour's digest",
            "wrap": True,
            "weight": "Bolder",
        }
    ]
    for item in input_data.items:
        facts = " · ".join(bit for bit in (item.severity, item.account) if bit)
        text = f"**{item.title}**" + (f"\n\n{facts}" if facts else "")
        body.append({"type": "TextBlock", "text": text, "wrap": True})
        details_url = item_details_url(item, UTM_SOURCE)
        if details_url:
            body.append(
                {"type": "ActionSet", "actions": [{"type": "Action.OpenUrl", "title": "Details", "url": details_url}]}
            )

    card = {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.2",  # ActionSet needs 1.2+
        "msteams": {"width": "Full"},
        "body": body,
    }
    if input_data.overflow_count > 0:
        body.append(
            {"type": "TextBlock", "text": f"+{input_data.overflow_count} more findings", "wrap": True, "isSubtle": True}
        )
        card["actions"] = [
            {"type": "Action.OpenUrl", "title": "View All Findings", "url": view_all_url(input_data, UTM_SOURCE)}
        ]

    return card
