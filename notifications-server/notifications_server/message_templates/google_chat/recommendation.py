from enum import Enum
from typing import List, Dict, Any, Optional, Union
from pydantic import BaseModel
from uuid import UUID

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.base import Emojis
import logging

LOG = logging.getLogger(__name__)


class RecommendationResolution(BaseModel):
    resolver: str
    type: str
    type_reference: str
    status: str
    status_message: str


class RecommendationSummary(BaseModel):
    id: Optional[str] = None
    recommendation_id: Optional[str] = None
    summary: Optional[str] = None
    status: str = ""
    savings: Optional[float] = None
    severity: Optional[str] = None
    resolution: Optional[RecommendationResolution] = None


class RecommendationParams(BaseModel):
    account_id: str
    account_name: str = ""
    category: str = ""
    rule_name: str = ""
    rule_description: Optional[str] = None
    total_estimated_savings: Optional[float] = None
    total_new: Optional[int] = None
    total_archived: Optional[int] = None
    total_affected_resources: Optional[int] = None
    recommendations: List[RecommendationSummary] = []


class RecommendationTabs(Enum):
    RIGHTSIZING = 1
    SECURITY = 5

    @classmethod
    def get_value(cls, key: str) -> int:
        return cls[key].value


def _format_status_text(status: str) -> str:
    lower = status.lower()
    if lower == "open":
        return f"{Emojis.New.value} Open"
    if lower == "resolved":
        return f"{Emojis.Resolved.value} Resolved"
    return f"{Emojis.ThumbsUp.value} {status}"


def _append_optional(lines: List[str], value: Any, template: Union[str, callable]) -> None:
    if value is None:
        return

    if callable(template):
        lines.append(template(value))
    else:
        lines.append(template.format(value))


def _format_totals(new: Optional[int], resolved: Optional[int]) -> str:
    parts = []
    if new:
        parts.append(f"{new} new")
    if resolved:
        parts.append(f"{resolved} resolved")
    return " and ".join(parts) if parts else "0 recommendations"


def _format_single_rec(rec: RecommendationSummary, idx: int) -> List[str]:
    if not rec.summary:
        return []

    chunk: List[str] = [f"{idx}. *{rec.summary}*"]

    if rec.savings and rec.savings > 0:
        chunk.append(f"- *Savings*: ${rec.savings}/month")
    if rec.severity:
        chunk.append(f"- *Severity*: {rec.severity}")
    chunk.append(f"- *Status*: {_format_status_text(rec.status)}")

    if rec.resolution:
        res = rec.resolution
        chunk.extend(
            [
                f"- *Resolver*: {res.resolver}",
                f"- *Type*: {res.type}",
                f"- *Reference*: {res.type_reference}",
                f"- *Resolution Status*: {res.status}",
                f"- *Message*: {res.status_message}",
            ]
        )

    chunk.append("")
    return chunk


def _append_grouped_recs(
    lines: List[str],
    recommendations: List[RecommendationSummary],
    max_items: int,
) -> int:
    severity_order = {"critical": 1, "high": 2}
    groups: Dict[str, List[RecommendationSummary]] = {}
    for rec in recommendations[:max_items]:
        key = rec.severity.capitalize() if rec.severity else "Other"
        groups.setdefault(key, []).append(rec)
    sorted_keys = sorted(groups.keys(), key=lambda k: severity_order.get(k.lower(), 99))

    counter = 1
    for key in sorted_keys:
        lines.append(f"*{key} Severity:*")
        for rec in groups[key]:
            rec_block = _format_single_rec(rec, counter)
            if rec_block:
                lines.extend(rec_block)
            counter += 1
    return counter - 1


def get_gchat_recommendation_template(params: RecommendationParams, max_items: int = 5) -> Dict[str, Any]:
    lines: List[str] = [f"🚀 *Kubernetes {params.category} Recommendations* for *{params.account_name}*"]

    _append_optional(lines, params.rule_description, "*Rule Description*: {}")
    _append_optional(
        lines,
        params.total_estimated_savings,
        "*Total Estimated Savings*: ${}/month".format,
    )

    totals_text = _format_totals(params.total_new, params.total_archived)

    view_all_url = (
        f"{public_ip()}/kubernetes/details/{params.account_id}"
        f"?tab={RecommendationTabs.get_value(params.category.upper())}"
        f"&accountId={params.account_id}"
    )
    affected_suffix = (
        f" *{params.total_affected_resources} affected resources.*" if params.total_affected_resources else ""
    )
    lines.append(f"Found *{totals_text}*.{affected_suffix} View all recommendations: {view_all_url}")
    lines.append("-" * 25)

    if params.category.lower() == "security":
        _append_grouped_recs(lines, params.recommendations, max_items)
    else:
        for idx, rec in enumerate(params.recommendations[:max_items], start=1):
            rec_block = _format_single_rec(rec, idx)
            if rec_block:
                lines.extend(rec_block)

    remaining = len(params.recommendations) - max_items
    if remaining > 0:
        lines.append(f"and *{remaining} more* recommendations. See all: {view_all_url}")

    lines.append(f"View more details on {settings.urls.branding_link('gchat')}")

    text = "\n".join(lines).strip()
    return {"text": text}
