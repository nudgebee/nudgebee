import logging
from typing import Optional

from notifications_server.configs.settings import URLRoutes, settings
from notifications_server.message_templates.slack.generic import GenericMessageParams, WorkflowMetadata
from notifications_server.utils.transformer import Transformer

LOG = logging.getLogger(__name__)

# Google Chat rejects a message over ~32,000 bytes outright.
GOOGLE_CHAT_SIZE_LIMIT_BYTES = 32_000


def get_google_chat_generic_message_template(generic_params: GenericMessageParams) -> str:
    footer = _footer(generic_params.workflow_metadata)
    full = generic_params.message + footer
    if len(full.encode()) <= GOOGLE_CHAT_SIZE_LIMIT_BYTES:
        return full

    # Budget the message against whatever room the footer actually leaves,
    # rather than a fixed guess — an unusually long workflow name or
    # triggerer must not be able to push the total back over the limit.
    suffix = _full_output_suffix(generic_params.workflow_metadata)
    message_limit = max(GOOGLE_CHAT_SIZE_LIMIT_BYTES - len(footer.encode()), 0)
    truncated = Transformer.apply_byte_length_limit(generic_params.message, message_limit, suffix)
    return truncated + footer


def _footer(metadata: Optional[WorkflowMetadata]) -> str:
    if not metadata or not metadata.workflow_name:
        return ""
    footer_parts = [f"Automation: {_render_automation_label(metadata)}"]
    if metadata.triggered_by:
        footer_parts.append(f"Triggered by: {metadata.triggered_by}")
    return "\n---\n" + " | ".join(footer_parts)


def _full_output_suffix(metadata: Optional[WorkflowMetadata]) -> str:
    if metadata and metadata.workflow_id and settings.base_url:
        url = settings.urls.workflow_url(
            metadata.workflow_id,
            account_id=metadata.account_id,
            utm_source=URLRoutes.UTMSource.GCHAT,
        )
        return f"\n\n… <{url}|View full output in {settings.urls.branding_name}>"
    return "..."


def _render_automation_label(metadata: WorkflowMetadata) -> str:
    name = metadata.workflow_name or ""
    if metadata.workflow_id and settings.base_url:
        url = settings.urls.workflow_url(
            metadata.workflow_id,
            account_id=metadata.account_id,
            utm_source=URLRoutes.UTMSource.GCHAT,
        )
        return f"<{url}|{name}>"
    return name
