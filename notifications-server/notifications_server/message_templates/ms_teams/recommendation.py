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
    """
    Format status text with corresponding emoji for Teams.
    """
    lower = status.lower()
    if lower == "open":
        return f"{Emojis.New.value} Open"
    if lower == "resolved":
        return f"{Emojis.Resolved.value} Resolved"
    return f"{Emojis.ThumbsUp.value} {status}"


def _create_summary_container(recommendation: RecommendationSummary, idx: int) -> Dict[str, Any]:
    facts: List[Dict[str, str]] = []
    if recommendation.savings and recommendation.savings > 0:
        facts.append({"title": "Savings", "value": f"${recommendation.savings}/month"})
    if recommendation.severity:
        facts.append({"title": "Severity", "value": recommendation.severity})
    facts.append({"title": "Status", "value": _format_status_text(recommendation.status)})

    if recommendation.resolution:
        res: RecommendationResolution = recommendation.resolution
        facts.extend(
            [
                {"title": "Resolver", "value": res.resolver},
                {"title": "Type", "value": res.type},
                {"title": "Reference", "value": res.type_reference},
                {"title": "Resolution Status", "value": res.status},
                {"title": "Message", "value": res.status_message},
            ]
        )

    return {
        "type": "Container",
        "items": [
            {"type": "TextBlock", "text": f"{idx}. **{recommendation.summary}**", "wrap": True},
            {"type": "FactSet", "facts": facts},
        ],
    }


def _build_header_blocks(params: RecommendationParams) -> List[Dict[str, Any]]:
    blocks: List[Dict[str, Any]] = [
        {
            "type": "TextBlock",
            "text": f"🚀 Kubernetes {params.category} Recommendations",
            "size": "Large",
            "weight": "Bolder",
            "wrap": True,
        },
        {
            "type": "TextBlock",
            "text": f"**Account:** [{params.account_name}]({public_ip()}/kubernetes/details/{params.account_id})",
            "wrap": True,
        },
    ]

    if params.total_estimated_savings:
        blocks.append(
            {
                "type": "TextBlock",
                "text": f"**Total Estimated Savings:** ${params.total_estimated_savings}/month",
                "wrap": True,
            }
        )

    if params.rule_description:
        blocks.append({"type": "TextBlock", "text": f"**Rule Description:** {params.rule_description}", "wrap": True})

    blocks.append({"type": "TextBlock", "text": "-----", "separator": True})
    return blocks


def _build_totals_block(params: RecommendationParams) -> Dict[str, Any]:
    parts: List[str] = []
    if params.total_new:
        parts.append(f"**{params.total_new} new**")
    if params.total_archived:
        parts.append(f"**{params.total_archived} resolved**")
    summary_text = " and ".join(parts)
    link = (
        f"{public_ip()}/kubernetes/details/{params.account_id}"
        f"?tab={RecommendationTabs.get_value(params.category.upper())}&accountId={params.account_id}"
    )
    affected_text = f" {params.total_affected_resources} affected resources." if params.total_affected_resources else ""
    return {
        "type": "TextBlock",
        "text": f"We found {summary_text} recommendations.{affected_text} [View all recommendations]({link})",
        "wrap": True,
    }


def _build_grouped_security_blocks(filtered: List[RecommendationSummary]) -> List[Dict[str, Any]]:
    severity_order = {"critical": 1, "high": 2}
    groups: Dict[str, List[RecommendationSummary]] = {}
    for rec in filtered:
        key = rec.severity.capitalize() if rec.severity else "Other"
        groups.setdefault(key, []).append(rec)
    sorted_keys = sorted(groups.keys(), key=lambda k: severity_order.get(k.lower(), 99))

    blocks: List[Dict[str, Any]] = []
    counter = 1
    for key in sorted_keys:
        blocks.append({"type": "TextBlock", "text": f"**{key} Severity:**", "weight": "Bolder", "wrap": True})
        for rec in groups[key]:
            blocks.append(_create_summary_container(rec, counter))
            counter += 1
    return blocks


def get_teams_recommendation_message_template(params: RecommendationParams) -> Dict[str, Any]:
    body: List[Dict[str, Any]] = _build_header_blocks(params)
    body.append(_build_totals_block(params))

    filtered = [rec for rec in params.recommendations if rec.summary]

    if params.category.lower() == "security":
        body.extend(_build_grouped_security_blocks(filtered))
    else:
        for idx, rec in enumerate(filtered, start=1):
            body.append(_create_summary_container(rec, idx))

    body.append(
        {
            "type": "TextBlock",
            "text": f"View more details on {settings.urls.branding_link('markdown')}",
            "wrap": True,
        }
    )

    return {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.2",
        "msteams": {"width": "Full"},
        "body": body,
    }
