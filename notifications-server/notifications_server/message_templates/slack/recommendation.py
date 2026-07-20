import logging
from enum import Enum
from typing import List, Dict, Any, Optional

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_SAVINGS,
    format_savings,
    header_block,
    legacy_attachment,
    link_button,
    neutral_footer_attachment,
)

LOG = logging.getLogger(__name__)

SECURITY_SEVERITY_FILTER = {"high", "critical"}

MAX_RECOMMENDATION_ITEMS = 12


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


def _tab_for_category(category: str) -> str:
    try:
        return RecommendationTabs.get_value(category.upper())
    except KeyError:
        return RecommendationTabs.RIGHTSIZING.value


def _intro_line(params: RecommendationParams) -> str:
    parts = []
    if params.total_new:
        parts.append(f"*{params.total_new} new*")
    if params.total_archived:
        parts.append(f"*{params.total_archived} resolved*")
    intro = f"We found {' and '.join(parts)} recommendations." if parts else ""
    if params.total_affected_resources:
        intro += f" *{params.total_affected_resources} affected resources.*"
    return intro.strip()


def _item_line(idx: int, rec: RecommendationSummary) -> str:
    extras = []
    if rec.savings and rec.savings > 0:
        extras.append(f"Save {format_savings(rec.savings)}/mo")
    if rec.severity:
        extras.append(rec.severity)
    if rec.status:
        extras.append(rec.status)
    suffix = f" · {' · '.join(extras)}" if extras else ""
    return f"*{idx}. {rec.summary}*{suffix}"


def get_recommendation_message_template(params: RecommendationParams) -> Dict[str, Any]:
    filtered = [rec for rec in params.recommendations if rec.summary]
    headline = " ".join(f"Kubernetes {params.category} recommendations".split())
    tab = _tab_for_category(params.category)
    url = f"{public_ip()}/kubernetes/details/{params.account_id}?accountId={params.account_id}&utm=slack#{tab}"

    lines = []
    intro = _intro_line(params)
    if intro:
        lines.append(intro)
    for idx, rec in enumerate(filtered[:MAX_RECOMMENDATION_ITEMS], 1):
        lines.append(_item_line(idx, rec))
    remaining = len(filtered) - MAX_RECOMMENDATION_ITEMS
    if remaining > 0:
        lines.append(f"_+{remaining} more recommendations_")

    attachments = [
        legacy_attachment(STRIPE_SAVINGS, headline, text="\n".join(lines)),
        neutral_footer_attachment(actions=[link_button("View recommendations", url, style="primary")]),
    ]
    return {"blocks": [header_block(headline)], "attachments": attachments, "unfurl_links": False}
