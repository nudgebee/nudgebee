"""Discord renderer for finding notifications.

Mirrors the Slack finding card (message_templates/slack/finding.py): a clickable,
severity-coloured title with compact Cluster / Resource / Priority / Source
fields plus a View Details link, returned as the {"content", "embeds"} payload
consumed by DiscordClient.chat_post.
"""

import logging
from datetime import datetime
from typing import Any, Dict

from notifications_server.configs.settings import settings
from notifications_server.message_templates.discord.embed_utils import clamp_embeds
from notifications_server.utils.transformer import Transformer

LOG = logging.getLogger(__name__)

# Match the Slack finding card's severity stripe (STRIPE_CRITICAL / STRIPE_HIGH)
# so a finding renders in the same colour on both platforms.
FINDING_COLOR_CRITICAL = 0xC93A36  # red, HIGH priority
FINDING_COLOR_HIGH = 0xD97A2B  # orange, everything else
MAX_EVIDENCE_FIELDS = 5


def _format_value(value: Any) -> str:
    """Format an evidence value into a Markdown string for Discord."""
    if isinstance(value, (dict, list)):
        truncated = Transformer.smart_truncate_json(value, max_length=1000)
        return f"```json\n{truncated}\n```"
    elif isinstance(value, str) and "\n" in value:
        return f"```\n{Transformer.apply_length_limit(value, 1000)}\n```"
    else:
        return f"`{Transformer.apply_length_limit(str(value), 1000)}`"


def _add_evidence_fields(embed: Dict[str, Any], finding: Dict[str, Any], is_cloud: bool) -> None:
    """Append up to MAX_EVIDENCE_FIELDS evidence blocks to the embed as fields."""
    evidences = finding.get("evidences", [])
    if not (evidences and isinstance(evidences, list)):
        return

    for evidence in evidences[:MAX_EVIDENCE_FIELDS]:
        if not isinstance(evidence, dict):
            continue

        data = evidence.get("data")
        if not data:
            continue

        evidence_type = evidence.get("type")
        # Field values are left un-sliced: clamp_embeds() truncates them to
        # Discord's per-field limit with an ellipsis, which a hard [:1024] here
        # would pre-empt.
        if evidence_type == "table":
            if not isinstance(data, dict) or "headers" not in data or "rows" not in data:
                continue
            table_md = Transformer.json_to_markdown_table(data)
            if table_md and table_md.strip():
                table_name = data.get("table_name", "Data Table")
                embed["fields"].append({"name": table_name, "value": table_md, "inline": False})
        elif evidence_type in {"markdown", "header"}:
            text = str(data.get("data", data) if isinstance(data, dict) else data)
            if text and text.strip():
                embed["fields"].append({"name": "Details", "value": text, "inline": False})
        elif evidence_type == "json" and is_cloud:
            val = _format_value(data.get("data", data) if isinstance(data, dict) else data)
            if val and val.strip():
                embed["fields"].append({"name": "Cloud Data", "value": val, "inline": False})


def get_discord_finding_message(finding: Dict[str, Any]) -> Dict[str, Any]:
    """Format a finding into a Discord message payload containing 'content' and 'embeds'."""
    title = finding.get("title") or "NudgeBee Finding"
    finding_id = finding.get("id", "")
    service_key = finding.get("service_key") or ""
    is_cloud = service_key.startswith("arn") or "aws" in service_key

    cluster = str(finding.get("cluster") or "").strip()
    subject_name = str(finding.get("subject_name") or "").strip()
    subject_namespace = str(finding.get("subject_namespace") or "").strip() or "default"
    cloud_account_id = finding.get("cloud_account_id", "")
    priority = str(finding.get("priority") or "").strip()
    source = str(finding.get("source") or "").strip()

    created_at_value = finding.get("starts_at") if is_cloud else finding.get("created_at")
    timestamp = ""
    try:
        if isinstance(created_at_value, str):
            timestamp = datetime.fromisoformat(created_at_value.replace("Z", "+00:00")).isoformat()
        elif isinstance(created_at_value, datetime):
            timestamp = created_at_value.isoformat()
    except Exception as e:
        LOG.debug(f"Unable to parse finding date, exception= {e}")

    investigate_url = settings.urls.investigate_url(
        account_id=cloud_account_id,
        finding_id=finding_id,
        utm_source="discord",
    )

    # Each attribute is its own embed field rather than a hand-built
    # "**Label:** `value`" description block: an empty value there left a dangling
    # backtick that desynced Discord's inline-code parser and rendered the
    # remaining labels literally. Empty fields are dropped instead.
    resource = f"{subject_namespace}/{subject_name}" if subject_name else subject_namespace
    fields = [
        {"name": name, "value": value, "inline": True}
        for name, value in (
            ("Cluster", cluster),
            ("Resource", resource),
            ("Priority", priority.title() if priority else ""),
            ("Source", source.replace("_", " ").title() if source else ""),
        )
        if value
    ]

    color = FINDING_COLOR_CRITICAL if priority.upper() == "HIGH" else FINDING_COLOR_HIGH
    embed: Dict[str, Any] = {
        "title": title,
        "url": investigate_url,
        "description": f"[🔍 View Details in NudgeBee]({investigate_url})",
        "color": color,
        "fields": fields,
    }
    if timestamp:
        embed["timestamp"] = timestamp

    _add_evidence_fields(embed, finding, is_cloud)

    return {"content": "", "embeds": clamp_embeds([embed])}
