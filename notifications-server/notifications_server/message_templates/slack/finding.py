import collections
import logging
from datetime import datetime
from typing import List

from notifications_server.configs.settings import (
    SLACK_TABLE_COLUMNS_LIMIT,
    DEFAULT_EVIDENCES,
    MAX_DETAILED_EVIDENCES,
    settings,
    URLRoutes,
)
from notifications_server.message_templates.base import BaseBlock
from notifications_server.message_templates.blocks import (
    MarkdownBlock,
    CallbackBlock,
    CallbackChoice,
    DividerBlock,
    FileBlock,
    ActionListBlock,
    LinksBlock,
    LinkProp,
    ActionElement,
    ListBlock,
)
from notifications_server.services.actions import AskAIParams
from notifications_server.services.events import Events
from notifications_server.utils.transformer import Transformer

LOG = logging.getLogger(__name__)


def add_callback(title, action, action_params):
    callback = CallbackBlock({title: CallbackChoice(action=action, action_params=action_params)})
    return callback


def add_evidences(blocks, finding, is_cloud):
    aggregation_key = finding.get("aggregation_key", "")
    evidences = finding.get("evidences") or []

    if aggregation_key == "query_failure":
        _process_query_evidences(blocks, finding)
    else:
        if not evidences or not isinstance(evidences, list):
            return

        evidence_count = len(evidences)

        for evidence in evidences[:MAX_DETAILED_EVIDENCES]:
            add_individual_evidence(blocks, evidence, is_cloud)

        if evidence_count > MAX_DETAILED_EVIDENCES:
            remaining_count = evidence_count - MAX_DETAILED_EVIDENCES
            blocks.append(MarkdownBlock(f"_... and {remaining_count} more evidence(s)_"))


def _process_query_evidences(blocks, finding):
    for key, value in (finding.get("evidences") or {}).items():
        if isinstance(value, collections.abc.Sequence) and not isinstance(value, (str, bytes)) and any(value):
            value = ", ".join(str(v) for v in value if v is not None)
            formatted_value = f"```{value}```" if key == "statement" else value
            blocks.append(MarkdownBlock(f"*{key}:* {formatted_value}"))


def add_individual_evidence(blocks, evidence, is_cloud):
    if evidence is None:
        return

    if "data" in evidence:
        data = evidence["data"]
        type_ = evidence.get("type")
        process_evidences(blocks, data, type_, is_cloud)
    else:
        LOG.debug("No key `data` in evidences %s", evidence)


def filter_label_data(data):
    data["rows"] = [row for row in data.get("rows", []) if row[0] in DEFAULT_EVIDENCES]
    return data


def process_evidences(blocks, data, type_, is_cloud):
    if type_ in {"markdown", "header"}:
        add_text_evidences(blocks, data)

    elif type_ in {"file", "gz"} or "filename" in data:
        LOG.debug("Disabled file log attachments")
        # FindingMessageBuilder.add_log_attachment(evidence, blocks, data, type_)

    elif type_ == "table":
        if len(data["headers"]) <= SLACK_TABLE_COLUMNS_LIMIT:
            data = filter_label_data(data)
        blocks.extend(Transformer.json_to_slack_blocks(data, type_))

    elif type_ == "json" and is_cloud:
        add_json_evidences(blocks, data)

    elif type_ is None and data.get("file_type") == "structured_data":
        blocks.extend(Transformer.json_to_slack_blocks(data.get("data"), type_))

    else:
        blocks.append(data)


def add_text_evidences(blocks, data):
    if isinstance(data, dict) and "data" in data:
        data = data["data"]
    blocks.append(MarkdownBlock(Transformer.to_slack_markdown_link(data)))


def add_json_evidences(blocks, data):
    if isinstance(data, dict) and "data" in data:
        data = data["data"]

    # Use smart truncation for JSON data
    # Increased to 1800 to show more useful details while staying within Slack limits
    truncated = Transformer.smart_truncate_json(data, max_length=1800)
    blocks.append(MarkdownBlock(Transformer.to_slack_markdown_link(f"```{truncated}```")))


def add_log_attachment(evidence, blocks, data, type_):
    if type_ == "gz_":
        content = Transformer.extract_text_from_base64_gz(data)
        filename = evidence.get("filename") or "log_dump.txt"
        blocks.append(FileBlock(filename, content))
    else:
        blocks.append(FileBlock(data.get("filename"), data.get("data")))


def _separate_blocks(blocks: List[BaseBlock]) -> tuple:
    """Separate blocks into file blocks and other blocks, handling SVG conversions."""
    file_blocks = []
    other_blocks = []
    for block in blocks:
        if isinstance(block, FileBlock):
            file_blocks.append(block)
        else:
            other_blocks.append(block)
    return file_blocks, other_blocks


def _create_evidence_attachment(evidence_slack_blocks: List) -> dict:
    """Create an attachment dict for evidence blocks."""
    return {
        "color": "#ff6b6b",
        "blocks": evidence_slack_blocks,
        "fallback": "Evidence details",
    }


def _to_blocks(slack_app, report_blocks: List[BaseBlock], evidence_blocks: List[BaseBlock], title: str, installation):
    # Separate main blocks into file and other blocks
    file_blocks, other_blocks = _separate_blocks(report_blocks)

    # Convert wide tables to file blocks
    file_blocks.extend(Transformer.tableblock_to_fileblocks(other_blocks, SLACK_TABLE_COLUMNS_LIMIT))

    message = Transformer.prepare_slack_text(slack_app, installation, title, max_file_size_kb=100, files=file_blocks)

    # Convert main blocks to slack format
    output_blocks = [block for b in other_blocks for block in Transformer.to_slack(b)]

    # Process evidence blocks into attachment
    attachments = []
    if evidence_blocks:
        evidence_file_blocks, evidence_other_blocks = _separate_blocks(evidence_blocks)
        evidence_slack_blocks = [block for b in evidence_other_blocks for block in Transformer.to_slack(b)]

        if evidence_slack_blocks:
            attachments.append(_create_evidence_attachment(evidence_slack_blocks))

        file_blocks.extend(evidence_file_blocks)

    LOG.debug(
        f"--sending to slack--\ntitle:{title}\nblocks: {output_blocks}\nattachments:"
        f" {attachments}\nmessage:{message}"
    )
    return message, output_blocks, attachments


def get_slack_finding_message(slack_app, installation, finding):
    title = finding.get("title")
    finding_id = finding.get("id")
    service_key = finding.get("service_key", "")
    is_cloud = service_key.startswith("arn") or "aws" in service_key

    # Extract relevant finding details
    cluster = finding.get("cluster")

    try:
        if is_cloud:
            created_at_value = finding.get("starts_at")
        else:
            created_at_value = finding.get("created_at")

        if isinstance(created_at_value, str):
            created_at = datetime.fromisoformat(created_at_value).timestamp()
        elif isinstance(created_at_value, datetime):
            created_at = created_at_value.timestamp()
        else:
            created_at = None
    except Exception as e:
        LOG.debug(f"Unable to parse finding date, exception= {e}")
        created_at = None

    subject_name = finding.get("subject_name")
    subject_namespace = finding.get("subject_namespace") or "default"
    cloud_account_id = finding.get("cloud_account_id")

    blocks: List[BaseBlock] = [MarkdownBlock(text=f"{title}")]

    meta = [
        (
            f"*Reported At:* <!date^{int(created_at)}^{{date_short_pretty}} {{time}}|April 14th, 2024 12:00 PM>"
            if created_at
            else ""
        ),
        f"*Account:* *{cluster}*" if cluster else "",
        f"*Namespace:* {subject_namespace}",
        f"*Workload: {subject_name}*",
    ]

    # Filter out empty strings in meta
    blocks.extend([ListBlock(items=[item for item in meta if item]), DividerBlock()])

    # Create separate list for evidence blocks
    evidence_blocks: List[BaseBlock] = []
    add_evidences(evidence_blocks, finding, is_cloud)

    tenant_id = str(installation.tenant_id)
    # Added "Ask nubi" callback
    ask_ai_callback = add_callback(
        "Ask Nubi to Analyse!",
        Events.query_llm_server,
        AskAIParams(
            channel_id="",
            team_id="",
            message_ts="channel",
            cluster_id=cloud_account_id,
            namespace=subject_namespace,
            event_id=finding_id,
            action_name="ask_ai",
            search_term=title,
            tenant_id=tenant_id,
        ),
    )

    # Build investigate URL using centralized settings
    investigate_url = settings.urls.investigate_url(
        account_id=cloud_account_id,
        finding_id=finding_id,
        utm_source=URLRoutes.UTMSource.SLACK,
    )

    # Add action buttons
    blocks.append(
        ActionListBlock(
            elements=[
                ActionElement(
                    element=LinksBlock(
                        links=[
                            LinkProp(
                                text="View Details",
                                url=investigate_url,
                            )
                        ]
                    )
                ),
                ActionElement(element=ask_ai_callback),
            ]
        )
    )

    return _to_blocks(slack_app, blocks, evidence_blocks, title, installation)
