from typing import Dict, Any, Optional

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    format_rule_name,
    format_savings,
)

BAND_DISPLAY_NAMES = {
    "Act Now": "Priority",
    "Critical": "Critical",
    "High": "High",
    "Medium": "Medium",
    "Low": "Low",
}


class ResolutionDetails(BaseModel):
    resolver: str = ""
    type: str = ""
    type_reference: str = ""
    status: str = ""
    status_message: str = ""


class RecommendationResolutionParams(BaseModel):
    organization_id: str = ""
    recommendation_id: str = ""
    resource_name: str = ""
    rule_name: str = ""
    category: str = ""
    account_id: str = ""
    account_name: str = ""
    finops_score: int = 0
    finops_band: str = "Low"
    estimated_savings: float = 0
    severity: str = "Medium"
    status: str = ""
    resolution: Optional[ResolutionDetails] = None
    base_url: str = ""


def get_recommendation_resolution_message_params(
    **params,
) -> RecommendationResolutionParams:
    raw_resolution = params.get("resolution")
    if isinstance(raw_resolution, dict):
        params["resolution"] = ResolutionDetails(**raw_resolution)
    return RecommendationResolutionParams(**params)


def get_recommendation_resolution_message_template(
    params: RecommendationResolutionParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    branding = settings.urls.branding_name
    blocks = []

    band_display = BAND_DISPLAY_NAMES.get(params.finops_band, params.finops_band)

    # Header
    blocks.append(
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": f"*{branding} Recommendation Resolved*",
            },
        }
    )
    blocks.append({"type": "divider"})

    # Resource info
    rule_display = format_rule_name(params.rule_name)
    blocks.append(
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": (
                    f"*{params.resource_name}*\n"
                    f"{rule_display} · {params.account_name}\n"
                    f"{band_display} · Score: {params.finops_score}/100 · "
                    f"Savings: {format_savings(params.estimated_savings)}/mo"
                ),
            },
        }
    )

    # Resolution details
    if params.resolution:
        res = params.resolution
        resolution_text = f"*Status:* {params.status}"
        if res.resolver:
            resolution_text += f"\n*Resolver:* {res.resolver}"
        if res.type:
            resolution_text += f"\n*Type:* {res.type}"
        if res.status:
            resolution_text += f"\n*Resolution Status:* {res.status}"
        if res.status_message:
            resolution_text += f"\n*Message:* {res.status_message}"

        blocks.append({"type": "divider"})
        blocks.append(
            {
                "type": "section",
                "text": {"type": "mrkdwn", "text": resolution_text},
            }
        )

    # Action buttons
    blocks.append({"type": "divider"})
    cta_url = f"{base_url}/optimise?id={params.recommendation_id}#summary"
    blocks.append(
        {
            "type": "actions",
            "elements": [
                {
                    "type": "button",
                    "text": {"type": "plain_text", "text": "View Details"},
                    "url": cta_url,
                },
                {
                    "type": "button",
                    "text": {"type": "plain_text", "text": "View All Recommendations"},
                    "url": f"{base_url}/optimise?utm=slack#recommendations",
                },
            ],
        }
    )

    fallback = f"Resolved: {params.resource_name} — {format_rule_name(params.rule_name)}"

    return {
        "text": fallback,
        "blocks": blocks[:50],
        "unfurl_links": False,
    }
