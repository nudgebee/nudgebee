from typing import Dict, Any, List

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_resolution import (
    RecommendationResolutionParams,
)
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


def get_teams_recommendation_resolution_template(
    params: RecommendationResolutionParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    branding = settings.urls.branding_name
    body: List[Dict[str, Any]] = []
    band_display = BAND_DISPLAY_NAMES.get(params.finops_band, params.finops_band)

    # Header
    body.append(
        {
            "type": "TextBlock",
            "text": f"{branding} Recommendation Resolved",
            "size": "Large",
            "weight": "Bolder",
            "color": "Good",
            "wrap": True,
        }
    )

    # Facts
    facts = [
        {"title": "Resource", "value": params.resource_name},
        {"title": "Rule", "value": format_rule_name(params.rule_name)},
        {"title": "Account", "value": params.account_name},
        {"title": "Band", "value": f"{band_display} ({params.finops_score}/100)"},
        {"title": "Savings", "value": f"{format_savings(params.estimated_savings)}/mo"},
        {"title": "Status", "value": params.status},
    ]

    if params.resolution:
        if params.resolution.resolver:
            facts.append({"title": "Resolver", "value": params.resolution.resolver})
        if params.resolution.status:
            facts.append({"title": "Resolution Status", "value": params.resolution.status})
        if params.resolution.status_message:
            facts.append({"title": "Message", "value": params.resolution.status_message})

    body.append({"type": "FactSet", "facts": facts})

    cta_url = f"{base_url}/optimize/summary?id={params.recommendation_id}"

    return {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.2",
        "msteams": {"width": "Full"},
        "body": body,
        "actions": [
            {
                "type": "Action.OpenUrl",
                "title": "View Details",
                "url": cta_url,
            },
            {
                "type": "Action.OpenUrl",
                "title": "View All Recommendations",
                "url": f"{base_url}/optimize/summary?utm=teams",
            },
        ],
    }
