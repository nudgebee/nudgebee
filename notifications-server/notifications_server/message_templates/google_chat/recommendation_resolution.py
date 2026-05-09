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


def get_gchat_recommendation_resolution_template(
    params: RecommendationResolutionParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    branding = settings.urls.branding_name
    lines: List[str] = []
    band_display = BAND_DISPLAY_NAMES.get(params.finops_band, params.finops_band)

    lines.append(f"*{branding} Recommendation Resolved*")
    lines.append("-" * 25)
    lines.append(f"*{params.resource_name}*")
    lines.append(f"{format_rule_name(params.rule_name)} \u00b7 {params.account_name}")
    lines.append(
        f"{band_display} \u00b7 Score: {params.finops_score}/100 \u00b7 "
        f"Savings: {format_savings(params.estimated_savings)}/mo"
    )
    lines.append("")
    lines.append(f"Status: {params.status}")

    if params.resolution:
        if params.resolution.resolver:
            lines.append(f"Resolver: {params.resolution.resolver}")
        if params.resolution.status:
            lines.append(f"Resolution Status: {params.resolution.status}")
        if params.resolution.status_message:
            lines.append(f"Message: {params.resolution.status_message}")

    lines.append("")
    cta_url = f"{base_url}/optimize/summary?id={params.recommendation_id}"
    lines.append(f"View Details: {cta_url}")

    return {"text": "\n".join(lines)}
