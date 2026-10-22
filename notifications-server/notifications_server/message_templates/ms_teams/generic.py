import logging
from typing import Dict, Any, Optional

from notifications_server.configs.settings import URLRoutes, settings
from notifications_server.message_templates.slack.generic import GenericMessageParams, WorkflowMetadata
from notifications_server.utils.transformer import Transformer

LOG = logging.getLogger(__name__)

# An automation can post output long enough to exceed Teams' ~28 KB card
# limit on its own. Capped well under card_actions.CARD_SIZE_LIMIT so the
# footer and card structure always still fit alongside the truncated message.
MESSAGE_LIMIT_BYTES = 20_000


def get_ms_teams_generic_message_template(generic_params: GenericMessageParams) -> Dict[str, Any]:
    # Imported here, not at module level: message_templates/__init__.py pulls this
    # module in during notifications_server/__init__.py's own bootstrap, before
    # sync_engine exists — card_actions needs it at import time.
    from notifications_server.services.ms_teams import card_actions

    card = _build_card(generic_params, generic_params.message)
    if card_actions.fits(card):
        return card

    truncated = Transformer.apply_byte_length_limit(
        generic_params.message, MESSAGE_LIMIT_BYTES, _full_output_suffix(generic_params.workflow_metadata)
    )
    card = _build_card(generic_params, truncated)
    if not card_actions.fits(card):
        LOG.info("Teams generic message still too large after truncating the message")
    return card


def _build_card(generic_params: GenericMessageParams, message: str) -> Dict[str, Any]:
    card = {
        "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.4",
        "msteams": {"width": "full"},
        "body": [{"type": "TextBlock", "text": message, "wrap": True}],
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


def _full_output_suffix(metadata: Optional[WorkflowMetadata]) -> str:
    if metadata and metadata.workflow_id and settings.base_url:
        url = settings.urls.workflow_url(
            metadata.workflow_id,
            account_id=metadata.account_id,
            utm_source=URLRoutes.UTMSource.TEAMS,
        )
        return f"\n\n… [View full output in NudgeBee]({url})"
    return "..."


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
