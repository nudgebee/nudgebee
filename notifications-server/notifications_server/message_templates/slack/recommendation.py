import logging
from enum import Enum
from typing import List, Dict, Any, Optional

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings

LOG = logging.getLogger(__name__)

SECURITY_SEVERITY_FILTER = {"high", "critical"}


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
    RIGHTSIZING = "optimize/summary"
    SECURITY = "security/image-scan"

    @classmethod
    def get_value(cls, key: str) -> str:
        return cls[key].value


def get_recommendation_message_params(**params) -> Optional[RecommendationParams]:
    parsed = RecommendationParams(**params)

    if parsed.category.lower() == "security":
        parsed.recommendations = [
            rec for rec in parsed.recommendations if rec.severity and rec.severity.lower() in SECURITY_SEVERITY_FILTER
        ]
        parsed.total_new = len(parsed.recommendations)
        if not parsed.recommendations:
            return None

    return parsed


def create_status_text(recommendation: RecommendationSummary) -> str:
    return recommendation.status


def create_resolution_text(resolution: RecommendationResolution) -> str:
    return (
        "\n\t●  *Resolution:*"
        f"\n\t\t- *Resolver:* {resolution.resolver}"
        f"\n\t\t- *Type:* {resolution.type}"
        f"\n\t\t- *Reference:* {resolution.type_reference}"
        f"\n\t\t- *Status:* {resolution.status}"
        f"\n\t\t- *Message:* {resolution.status_message}"
    )


def create_summary_block(recommendation: RecommendationSummary, count: int) -> Dict[str, Any]:
    text = f"{count}. *{recommendation.summary}*"
    if recommendation.savings and recommendation.savings > 0:
        text += f"\n\t●  *Savings:* ${recommendation.savings}/month"
    if recommendation.severity is not None:
        text += f"\n\t●  *Severity:* {recommendation.severity}"
    text += f"\n\t●  *Status:* {create_status_text(recommendation)}"
    if recommendation.resolution:
        text += create_resolution_text(recommendation.resolution)
    return {
        "type": "section",
        "text": {
            "type": "mrkdwn",
            "text": text,
        },
    }


def _group_by_severity(
    recommendations: List[RecommendationSummary],
) -> List[Dict[str, Any]]:
    severity_order = {"critical": 1, "high": 2}
    groups: Dict[str, List[RecommendationSummary]] = {}
    for rec in recommendations:
        key = rec.severity.capitalize() if rec.severity else "Other"
        groups.setdefault(key, []).append(rec)

    sorted_keys = sorted(groups.keys(), key=lambda k: severity_order.get(k.lower(), 99))

    blocks: List[Dict[str, Any]] = []
    counter = 1
    for key in sorted_keys:
        blocks.append(
            {
                "type": "section",
                "text": {"type": "mrkdwn", "text": f"*{key} Severity:*"},
            }
        )
        for rec in groups[key]:
            blocks.append(create_summary_block(rec, counter))
            counter += 1
    return blocks


def get_summary_block(category: str, recommendations: List[RecommendationSummary]) -> List[Dict[str, Any]]:
    filtered = [rec for rec in recommendations if rec.summary is not None]
    if not filtered:
        return []

    if category.lower() == "security":
        grouped_blocks = _group_by_severity(filtered)
        return [
            {
                "type": "section",
                "text": {"type": "mrkdwn", "text": f"*Top {len(filtered)} Recommendations:*"},
            },
            *grouped_blocks,
        ]

    numbered_blocks = [create_summary_block(rec, idx + 1) for idx, rec in enumerate(filtered)]
    return [
        {
            "type": "section",
            "text": {"type": "mrkdwn", "text": f"*Top {len(numbered_blocks)} Recommendations:*"},
        },
        *numbered_blocks,
    ]


def get_title_blocks(params: RecommendationParams) -> List[Dict[str, Any]]:
    title_blocks = [
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": f"*Kubernetes {params.category} Recommendations*",
            },
        },
        {
            "type": "section",
            "fields": [
                {
                    "type": "mrkdwn",
                    "text": (
                        "*Account:*"
                        f" <{public_ip()}/kubernetes/details/{params.account_id}?utm=slack|{params.account_name}>"
                    ),
                },
            ],
        },
    ]

    if params.total_estimated_savings:
        title_blocks[1]["fields"].append(
            {
                "type": "mrkdwn",
                "text": f"*Total Estimated Savings:* ${params.total_estimated_savings}/month",
            }
        )

    if params.rule_description:
        return [
            *title_blocks,
            {
                "type": "section",
                "text": {"type": "mrkdwn", "text": f"*Rule Description:* {params.rule_description}"},
            },
            {"type": "divider"},
        ]
    else:
        return [*title_blocks, {"type": "divider"}]


def get_total_recommendations_block(params: RecommendationParams) -> Dict[str, Any]:
    tab = RecommendationTabs.get_value(params.category.upper())
    message_parts = []
    if params.total_new:
        message_parts.append(f"*{params.total_new} new*")
    if params.total_archived:
        message_parts.append(f"*{params.total_archived} resolved*")
    message_summary = " and ".join(message_parts)
    message = f"We found {message_summary} recommendations."
    if params.total_affected_resources:
        message += f" *{params.total_affected_resources} affected resources.*"
    text = (
        f"{message} To see all, click "
        f"<{public_ip()}/kubernetes/details/{params.account_id}?accountId={params.account_id}&utm=slack#{tab}|here>"
    )
    return {
        "type": "section",
        "text": {
            "type": "mrkdwn",
            "text": text,
        },
    }


def get_recommendation_message_template(params: RecommendationParams) -> Dict[str, Any]:
    blocks = [
        *get_title_blocks(params),
        get_total_recommendations_block(params),
        *get_summary_block(params.category, params.recommendations),
        {"type": "divider"},
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": f"View more details on {settings.urls.branding_link('slack')}",
            },
        },
    ]
    return {"text": "Recommendations", "blocks": blocks, "unfurl_links": False}
