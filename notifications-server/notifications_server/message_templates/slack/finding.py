import collections
import logging
import time
from datetime import datetime, timezone
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
    CallbackChoice,
    FileBlock,
)
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_CRITICAL,
    STRIPE_HIGH,
    STRIPE_SAVINGS,
)
from notifications_server.services.actions import AskAIParams
from notifications_server.services.events import Events
from notifications_server.utils.action_requests import (
    ActionRequestBody,
    ExternalActionRequest,
    sign_action_request,
)
from notifications_server.utils.callbacks import ExternalActionRequestBuilder
from notifications_server.utils.transformer import SLACK_SIGNIN_SECRET, Transformer

LOG = logging.getLogger(__name__)

# callback_id routed by SlackInteractiveActionsService for legacy attachment
# buttons (Slack sends interactive_message payloads for these, not
# block_actions; the value contract is identical).
FINDING_CALLBACK_ID = "nb_finding_actions"

# Wire identifier, not copy: cards already posted to Slack carry this value in
# their signed payloads, so it stays as-is while the labels read "Suppress"
# (matching the Triage Rules UI and the SUPPRESSED status it produces).
SUPPRESS_ACTION_NAME = "silence_finding"
SUPPRESS_THIS_DURATIONS = (("1h", 1), ("4h", 4), ("24h", 24), ("7d", 168))
SUPPRESS_ALL_DURATIONS = (("4h", 4), ("24h", 24), ("7d", 168))


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


def _blocks_to_mrkdwn(slack_blocks: List[dict]) -> str:
    """Flatten converted evidence blocks into one mrkdwn string. Slack stamps
    an "Added by {app}" byline under any attachment carrying Block Kit
    ``blocks``, so evidences ride a legacy text attachment instead. Evidence
    content only ever converts to section/context/header/divider blocks
    (Transformer.to_slack drops everything else)."""
    parts: List[str] = []
    for block in slack_blocks:
        block_type = block.get("type")
        if block_type == "section" and isinstance(block.get("text"), dict):
            parts.append(block["text"].get("text", ""))
        elif block_type == "context":
            parts.extend(el.get("text", "") for el in block.get("elements", []) if isinstance(el, dict))
        elif block_type == "header":
            parts.append(f"*{block.get('text', {}).get('text', '')}*")
    return "\n".join(p for p in parts if p)


def _signed_suppress_value(action_params):
    body = ActionRequestBody(
        timestamp=int(time.time()),
        action_name=SUPPRESS_ACTION_NAME,
        action_params=action_params,
        origin="callback",
    )
    return ExternalActionRequest(body=body, signature=sign_action_request(body, SLACK_SIGNIN_SECRET)).model_dump_json()


def _suppress_select_action(finding, tenant_id):
    """Legacy attachment menu offering time-boxed suppressions. Scope
    "fingerprint" quiets repeat firings of this exact alert; "alertname" quiets
    every alert sharing the aggregation key on the account. Each option value is
    a signed callback payload (same contract as the card buttons)."""
    fingerprint = str(finding.get("fingerprint") or "").strip()
    alertname = str(finding.get("aggregation_key") or "").strip()
    account_id = finding.get("cloud_account_id")
    if not account_id:
        return None

    def _options(scope, durations):
        options = []
        for label, hours in durations:
            value = _signed_suppress_value(
                {
                    "action_name": SUPPRESS_ACTION_NAME,
                    "event_id": finding.get("id"),
                    "fingerprint": fingerprint,
                    "alertname": alertname,
                    "account_id": account_id,
                    "tenant_id": tenant_id,
                    "scope": scope,
                    "duration_hours": hours,
                }
            )
            options.append({"text": f"Suppress {label}", "value": value})
        return options

    option_groups = []
    if fingerprint:
        option_groups.append({"text": "This alert only", "options": _options("fingerprint", SUPPRESS_THIS_DURATIONS)})
    if alertname:
        shown = alertname if len(alertname) <= 30 else alertname[:27] + "…"
        option_groups.append(
            {"text": f'All "{shown}" alerts', "options": _options("alertname", SUPPRESS_ALL_DURATIONS)}
        )
    if not option_groups:
        return None

    return {
        "type": "select",
        "name": SUPPRESS_ACTION_NAME,
        "text": "Suppress…",
        "option_groups": option_groups,
    }


def get_slack_finding_message(slack_app, installation, finding):
    """Hybrid finding card: ONE severity-striped legacy attachment — clickable
    title (title_link), evidence sentences, compact two-column fields, and all
    actions inside the card. The top-level message text and blocks stay EMPTY:
    with no blocks, any text renders as a duplicate heading, and
    blocks-in-attachments or footer/ts fields make Slack stamp the
    "Added by {app}" byline."""
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
    priority = str(finding.get("priority") or "").strip()

    # Evidence content becomes the card body; wide tables upload as files and
    # get linked from the card.
    evidence_blocks: List[BaseBlock] = []
    add_evidences(evidence_blocks, finding, is_cloud)
    file_blocks, other_evidence = _separate_blocks(evidence_blocks)
    evidence_slack_blocks = [block for b in other_evidence for block in Transformer.to_slack(b)]

    lines = []
    evidence_text = _blocks_to_mrkdwn(evidence_slack_blocks)
    if evidence_text:
        lines.append(evidence_text)

    reported = ""
    if created_at:
        try:
            ts = int(created_at)
            fallback_time = datetime.fromtimestamp(ts, tz=timezone.utc).strftime("%d %b %Y %I:%M %p UTC")
            reported = f"reported <!date^{ts}^{{date_short_pretty}} {{time}}|{fallback_time}>"
        except (TypeError, ValueError, OSError, OverflowError):
            reported = ""

    resource = f"{subject_namespace}/{subject_name}" if subject_name else subject_namespace
    source = str(finding.get("source") or "").strip()
    fields = [
        {"title": title_, "value": value, "short": True}
        for title_, value in (
            ("Cluster", cluster),
            ("Resource", resource),
            ("Priority", priority.title() if priority else ""),
            ("Source", source.replace("_", " ").title() if source else ""),
        )
        if value
    ]
    if reported:
        fields.append({"title": "", "value": reported, "short": False})

    if file_blocks:
        file_links = Transformer.prepare_slack_text(
            slack_app, installation, "", max_file_size_kb=100, files=file_blocks
        )
        if file_links and file_links != "empty-message":
            lines.append(file_links.strip())

    # Build investigate URL using centralized settings
    investigate_url = settings.urls.investigate_url(
        account_id=cloud_account_id,
        finding_id=finding_id,
        utm_source=URLRoutes.UTMSource.SLACK,
    )

    tenant_id = str(installation.tenant_id)
    ask_nubi_value = ExternalActionRequestBuilder.create_for_func(
        CallbackChoice(
            action=Events.query_llm_server,
            action_params=AskAIParams(
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
        ),
        "Ask Nubi to Analyse!",
        SLACK_SIGNIN_SECRET,
    ).model_dump_json()

    actions = [
        {"type": "button", "text": "View Details", "url": investigate_url, "style": "primary"},
        {"type": "button", "name": "ask_nubi", "text": "Ask Nubi to Analyse!", "value": ask_nubi_value},
    ]
    suppress_action = _suppress_select_action(finding, tenant_id)
    if suppress_action:
        actions.append(suppress_action)

    attachment = {
        "color": STRIPE_CRITICAL if priority.upper() == "HIGH" else STRIPE_HIGH,
        "fallback": title,
        "mrkdwn_in": ["text", "fields"],
        "title": title,
        "title_link": investigate_url,
        "text": "\n".join(lines),
        "fields": fields,
        "callback_id": FINDING_CALLBACK_ID,
        "actions": actions,
    }

    LOG.debug(f"--sending to slack--\ntitle:{title}\nattachment: {attachment}")
    return "", [], [attachment]


def _to_utc_datetime(value):
    try:
        if isinstance(value, datetime):
            parsed = value
        elif isinstance(value, str):
            parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        else:
            return None
    except ValueError:
        return None
    return parsed if parsed.tzinfo else parsed.replace(tzinfo=timezone.utc)


def _humanize_duration(delta):
    total_minutes = max(int(delta.total_seconds() // 60), 0)
    days, remainder = divmod(total_minutes, 1440)
    hours, minutes = divmod(remainder, 60)
    if days:
        return f"{days}d {hours}h" if hours else f"{days}d"
    if hours:
        return f"{hours}h {minutes}m" if minutes else f"{hours}h"
    return f"{minutes}m"


def _slack_time(moment):
    ts = int(moment.timestamp())
    fallback = moment.astimezone(timezone.utc).strftime("%I:%M %p UTC")
    return f"<!date^{ts}^{{time}}|{fallback}>"


def get_slack_resolved_finding_reply(finding):
    """Green closure reply threaded under the original alert message once the
    finding clears. Same attachment-only shape as the firing card (top-level
    text/blocks stay empty, no footer/ts — the byline constraints apply)."""
    title = finding.get("title")
    investigate_url = settings.urls.investigate_url(
        account_id=finding.get("cloud_account_id"),
        finding_id=finding.get("id"),
        utm_source=URLRoutes.UTMSource.SLACK,
    )

    started = _to_utc_datetime(finding.get("starts_at"))
    ended = _to_utc_datetime(finding.get("ends_at")) or datetime.now(timezone.utc)
    if started and ended >= started:
        line = (
            f"Cleared after {_humanize_duration(ended - started)}"
            f" (fired {_slack_time(started)} → resolved {_slack_time(ended)})."
        )
    else:
        line = f"Cleared at {_slack_time(ended)}."

    attachment = {
        "color": STRIPE_SAVINGS,
        "fallback": f"Resolved: {title}",
        "mrkdwn_in": ["text"],
        "title": f"Resolved: {title}",
        "title_link": investigate_url,
        "text": line,
    }
    return "", [], [attachment]
