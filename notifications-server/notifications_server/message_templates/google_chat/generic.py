from notifications_server.configs.settings import URLRoutes, settings
from notifications_server.message_templates.slack.generic import GenericMessageParams, WorkflowMetadata


def get_google_chat_generic_message_template(generic_params: GenericMessageParams) -> str:
    message = generic_params.message

    if generic_params.workflow_metadata and generic_params.workflow_metadata.workflow_name:
        meta = generic_params.workflow_metadata
        footer_parts = [f"Automation: {_render_automation_label(meta)}"]
        if meta.triggered_by:
            footer_parts.append(f"Triggered by: {meta.triggered_by}")
        message += "\n---\n" + " | ".join(footer_parts)

    return message


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
