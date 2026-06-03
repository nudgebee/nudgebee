from typing import Dict, Any

from notifications_server.configs.settings import URLRoutes, settings
from notifications_server.message_templates.slack.generic import GenericMessageParams, WorkflowMetadata


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
        footer_parts = [f"Automation: {_render_automation_label(meta)}"]
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


def _render_automation_label(metadata: WorkflowMetadata) -> str:
    name = metadata.workflow_name or ""
    if metadata.workflow_id and settings.base_url:
        url = settings.urls.workflow_url(
            metadata.workflow_id,
            account_id=metadata.account_id,
            utm_source=URLRoutes.UTMSource.TEAMS,
        )
        return f"[**{name}**]({url})"
    return f"**{name}**"
