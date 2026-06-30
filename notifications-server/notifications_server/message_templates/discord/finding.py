import logging
from datetime import datetime
from typing import List, Dict, Any

from notifications_server.configs.settings import settings, URLRoutes
from notifications_server.utils.transformer import Transformer

LOG = logging.getLogger(__name__)


def _format_value(value: Any) -> str:
    """Format an evidence value into a Markdown string for Discord."""
    if isinstance(value, dict) or isinstance(value, list):
        truncated = Transformer.smart_truncate_json(value, max_length=1000)
        return f"```json\n{truncated}\n```"
    elif isinstance(value, str) and "\n" in value:
        return f"```\n{Transformer.apply_length_limit(value, 1000)}\n```"
    else:
        return f"`{Transformer.apply_length_limit(str(value), 1000)}`"


def get_discord_finding_message(finding: Dict[str, Any]) -> Dict[str, Any]:
    """
    Format a finding into Discord message payload containing 'content' and 'embeds'.
    """
    title = finding.get("title", "NudgeBee Finding")
    finding_id = finding.get("id", "")
    service_key = finding.get("service_key") or ""
    is_cloud = service_key.startswith("arn") or "aws" in service_key

    cluster = finding.get("cluster", "Unknown")

    # Parse date
    created_at_value = finding.get("starts_at") if is_cloud else finding.get("created_at")
    timestamp = ""
    try:
        if isinstance(created_at_value, str):
            timestamp = datetime.fromisoformat(created_at_value.replace("Z", "+00:00")).isoformat()
        elif isinstance(created_at_value, datetime):
            timestamp = created_at_value.isoformat()
    except Exception as e:
        LOG.debug(f"Unable to parse finding date, exception= {e}")

    subject_name = finding.get("subject_name", "Unknown")
    subject_namespace = finding.get("subject_namespace", "default")
    cloud_account_id = finding.get("cloud_account_id", "")

    investigate_url = settings.urls.investigate_url(
        account_id=cloud_account_id,
        finding_id=finding_id,
        utm_source="discord",
    )

    description = (
        f"**Account:** `{cluster}`\n"
        f"**Namespace:** `{subject_namespace}`\n"
        f"**Workload:** `{subject_name}`\n\n"
        f"[🔍 View Details in NudgeBee]({investigate_url})"
    )

    embed = {"title": title, "description": description, "color": 15158332, "fields": []}  # Red-ish color

    if timestamp:
        embed["timestamp"] = timestamp

    # Add evidence fields
    evidences = finding.get("evidences", [])
    if evidences and isinstance(evidences, list):
        for evidence in evidences[:5]:  # Max 5 evidence fields to avoid limits
            if not isinstance(evidence, dict):
                continue

            data = evidence.get("data")
            if not data:
                continue

            evidence_type = evidence.get("type")
            if evidence_type == "table":
                table_md = Transformer.json_to_markdown_table(data)
                if table_md and table_md.strip():
                    table_name = data.get("table_name", "Data Table") if isinstance(data, dict) else "Data Table"
                    embed["fields"].append({"name": table_name, "value": table_md[:1024], "inline": False})
            elif evidence_type in {"markdown", "header"}:
                text = str(data.get("data", data) if isinstance(data, dict) else data)
                if text and text.strip():
                    embed["fields"].append({"name": "Details", "value": text[:1024], "inline": False})
            elif evidence_type == "json" and is_cloud:
                val = _format_value(data.get("data", data) if isinstance(data, dict) else data)
                if val and val.strip():
                    embed["fields"].append({"name": "Cloud Data", "value": val[:1024], "inline": False})

    return {"content": "", "embeds": [embed]}
