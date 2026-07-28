"""Discord renderer for generic (workflow / free-form) notifications.

Mirrors message_templates/google_chat/generic.py: the message body plus an
optional automation footer. Approval buttons are Slack-interactive only and
have no Discord equivalent. Returns the {"content", "embeds"} payload consumed
by DiscordClient.chat_post.
"""

from typing import Any, Dict

from notifications_server.configs.settings import settings
from notifications_server.message_templates.discord.embed_utils import clamp_embeds
from notifications_server.message_templates.slack.generic import GenericMessageParams, WorkflowMetadata

GENERIC_COLOR = 3447003  # blue (#3498db)


def _render_automation_label(metadata: WorkflowMetadata) -> str:
    name = metadata.workflow_name or ""
    if metadata.workflow_id and settings.base_url:
        url = settings.urls.workflow_url(
            metadata.workflow_id,
            account_id=metadata.account_id,
            utm_source="discord",
        )
        return f"[{name}]({url})"
    return name


def get_discord_generic_message_template(generic_params: GenericMessageParams) -> Dict[str, Any]:
    description = generic_params.message or ""

    if generic_params.workflow_metadata and generic_params.workflow_metadata.workflow_name:
        meta = generic_params.workflow_metadata
        footer_parts = [f"Automation: {_render_automation_label(meta)}"]
        if meta.triggered_by:
            footer_parts.append(f"Triggered by: {meta.triggered_by}")
        description += "\n---\n" + " | ".join(footer_parts)

    embed = {"description": description, "color": GENERIC_COLOR}
    return {"content": "", "embeds": clamp_embeds([embed])}
