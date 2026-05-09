from typing import Dict, Any

from notifications_server.message_templates.slack.generic import GenericMessageParams


def get_ms_teams_generic_message_template(generic_params: GenericMessageParams) -> Dict[str, Any]:
    card = {
        "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.4",
        "msteams": {"width": "full"},
        "body": [{"type": "TextBlock", "text": generic_params.message, "wrap": True}],
    }

    if generic_params.workflow_metadata and generic_params.workflow_metadata.workflow_name:
        meta = generic_params.workflow_metadata
        footer_parts = [f"Workflow: **{meta.workflow_name}**"]
        if meta.triggered_by:
            footer_parts.append(f"Triggered by: {meta.triggered_by}")
        card["body"].append(
            {
                "type": "TextBlock",
                "text": " | ".join(footer_parts),
                "wrap": True,
                "size": "Small",
                "color": "Light",
                "separator": True,
            }
        )

    return card
