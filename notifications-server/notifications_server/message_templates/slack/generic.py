import json
from typing import Optional, List

from pydantic import BaseModel
from notifications_server.utils.transformer import Transformer

MAX_SLACK_BLOCK_LENGTH = 3000
MAX_BLOCKS = 50
# Reserved when an approval block (1) and/or workflow footer (divider + context = 2) are appended.
APPROVAL_BLOCK_RESERVED = 1
WORKFLOW_FOOTER_BLOCKS_RESERVED = 2


class WorkflowMetadata(BaseModel):
    workflow_name: Optional[str] = None
    triggered_by: Optional[str] = None


_APPROVAL_FOOTER = "Your decision will resume the automation immediately."


class GenericMessageParams(BaseModel):
    message: str
    approval_token: Optional[str] = None
    approval_options: Optional[List[str]] = None
    workflow_name: Optional[str] = None
    run_id: Optional[str] = None
    requested_at: Optional[int] = None  # Unix seconds, rendered via Slack <!date^...>
    workflow_metadata: Optional[WorkflowMetadata] = None


def get_generic_message_params(**params) -> GenericMessageParams:
    return GenericMessageParams(**params)


def get_slack_generic_message_template(generic_params: GenericMessageParams) -> dict:
    is_approval = bool(generic_params.approval_token)

    # Build header (approval messages only)
    header_blocks: List[dict] = []
    if is_approval:
        header_blocks.append(_build_header_block())
        meta_block = _build_meta_fields_block(
            generic_params.workflow_name,
            generic_params.run_id,
            generic_params.requested_at,
        )
        if meta_block:
            header_blocks.append(meta_block)
            header_blocks.append({"type": "divider"})

    # Build trailing blocks (approval action / approval footer / workflow tracing footer)
    trailing_blocks: List[dict] = []
    approval_block = _build_approval_block(
        generic_params.approval_options,
        generic_params.approval_token,
    )
    if approval_block:
        trailing_blocks.append(approval_block)

    if is_approval:
        trailing_blocks.append(_build_context_block(_APPROVAL_FOOTER))
    else:
        workflow_footer = _build_workflow_footer(generic_params.workflow_metadata)
        if workflow_footer:
            trailing_blocks.extend(workflow_footer)

    # Reserve space for header + trailing so the footer never gets truncated
    body_budget = max(MAX_BLOCKS - len(header_blocks) - len(trailing_blocks), 1)
    body_blocks = _build_body_blocks(generic_params.message or "", is_approval, max_blocks=body_budget)

    blocks = header_blocks + body_blocks + trailing_blocks

    fallback = _fallback_text(generic_params, is_approval)

    result = {
        "text": fallback,
        "blocks": blocks[:MAX_BLOCKS],
        "unfurl_links": False,
    }

    # Store token in message metadata to avoid Slack's 256 char button value limit
    if generic_params.approval_token:
        result["metadata"] = {
            "event_type": "workflow_approval",
            "event_payload": {"token": generic_params.approval_token},
        }

    return result


def _build_header_block() -> dict:
    return {
        "type": "header",
        "text": {"type": "plain_text", "text": "Action required"},
    }


def _build_meta_fields_block(
    workflow_name: Optional[str],
    run_id: Optional[str],
    requested_at: Optional[int],
) -> Optional[dict]:
    fields: List[dict] = []
    if workflow_name:
        fields.append({"type": "mrkdwn", "text": f"*Automation*\n{workflow_name}"})
    if run_id:
        fields.append({"type": "mrkdwn", "text": f"*Run*\n`{run_id}`"})
    if requested_at:
        fields.append(
            {
                "type": "mrkdwn",
                "text": ("*Requested*\n" f"<!date^{int(requested_at)}^{{date_short_pretty}} at {{time}}|recently>"),
            }
        )

    if not fields:
        return None

    return {"type": "section", "fields": fields}


def _build_body_blocks(message: str, is_approval: bool, max_blocks: int = MAX_BLOCKS) -> List[dict]:
    slack_message = Transformer.markdown_to_slack_markdown(message)

    if is_approval and slack_message.strip():
        slack_message = "\n".join(f"> {line}" for line in slack_message.splitlines())

    if not slack_message:
        return []

    chunks = [
        slack_message[i : i + MAX_SLACK_BLOCK_LENGTH] for i in range(0, len(slack_message), MAX_SLACK_BLOCK_LENGTH)
    ]
    return [{"type": "section", "text": {"type": "mrkdwn", "text": chunk}} for chunk in chunks[:max_blocks]]


def _build_context_block(text: str) -> dict:
    return {
        "type": "context",
        "elements": [{"type": "mrkdwn", "text": text}],
    }


def _build_approval_block(options: Optional[List[str]], token: Optional[str]) -> Optional[dict]:
    if not options or not token:
        return None

    valid_options = [opt for opt in options if isinstance(opt, str) and opt.strip()]
    if not valid_options:
        return None

    # Token is stored in message metadata, not in button/dropdown values
    if len(valid_options) <= 3:
        elements = _build_buttons(valid_options)
    else:
        elements = [_build_dropdown(valid_options)]

    return {"type": "actions", "elements": elements} if elements else None


def _build_buttons(options: List[str]) -> List[dict]:
    return [
        {
            "type": "button",
            "text": {"type": "plain_text", "text": _format_label(option)},
            "value": _build_action_value(option),
            "action_id": f"workflow_approval_{option}",
        }
        for option in options
    ]


def _build_dropdown(options: List[str]) -> dict:
    return {
        "type": "static_select",
        "placeholder": {"type": "plain_text", "text": "Select an option"},
        "options": [
            {
                "text": {"type": "plain_text", "text": _format_label(option)},
                "value": option,
            }
            for option in options
        ],
        "action_id": "workflow_approval_action_select",
    }


def _format_label(option: str) -> str:
    return option.replace("_", " ").replace("-", " ").title()


def _build_action_value(option: str) -> str:
    # Token is retrieved from message metadata on the handler side
    return json.dumps(
        {
            "body": {
                "action_name": "workflow_approval_action",
                "action_params": {"status": option},
            }
        }
    )


def _fallback_text(params: GenericMessageParams, is_approval: bool) -> str:
    # Used by Slack for notifications / accessibility when blocks can't render.
    if is_approval:
        if params.workflow_name:
            return f"Action required — {params.workflow_name}"
        return "Action required"
    return (params.message or "")[:100]


def _build_workflow_footer(metadata: Optional[WorkflowMetadata]) -> Optional[List[dict]]:
    if not metadata or not metadata.workflow_name:
        return None

    footer_parts = [f"Automation: *{metadata.workflow_name}*"]
    if metadata.triggered_by:
        footer_parts.append(f"Triggered by: {metadata.triggered_by}")

    return [
        {"type": "divider"},
        {
            "type": "context",
            "elements": [{"type": "mrkdwn", "text": " | ".join(footer_parts)}],
        },
    ]
