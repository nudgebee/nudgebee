from notifications_server.message_templates.slack.generic import GenericMessageParams


def get_google_chat_generic_message_template(generic_params: GenericMessageParams) -> str:
    message = generic_params.message

    if generic_params.workflow_metadata and generic_params.workflow_metadata.workflow_name:
        meta = generic_params.workflow_metadata
        footer_parts = [f"Workflow: {meta.workflow_name}"]
        if meta.triggered_by:
            footer_parts.append(f"Triggered by: {meta.triggered_by}")
        message += "\n---\n" + " | ".join(footer_parts)

    return message
