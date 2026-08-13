from typing import Dict, Any, Optional

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_SAVINGS,
    format_rule_name,
    format_savings,
    header_block,
    legacy_attachment,
    link_button,
)


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


def _resolution_line(params: RecommendationResolutionParams) -> str:
    """One human sentence for how the item got resolved — replaces the old
    Status/Resolver/Type key-value rows."""
    res = params.resolution
    if not res:
        return f"Marked {params.status}" if params.status else ""
    if res.status_message:
        line = res.status_message
    else:
        verb = "Applied" if res.type == "applied" else "Resolved"
        if res.resolver == "auto":
            line = f"{verb} automatically"
        elif res.resolver:
            line = f"{verb} by {res.resolver}"
        else:
            line = verb
    if res.status and res.status.lower() not in ("success", "succeeded", "ok", "closed"):
        line += f" · {res.status}"
    return line


def get_recommendation_resolution_message_template(
    params: RecommendationResolutionParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    branding = settings.urls.branding_name
    rule_display = format_rule_name(params.rule_name)

    # Outcome-first headline: lead with the money when the change banked it;
    # dismissals never claim savings.
    if (params.status or "").lower() == "dismissed":
        headline = f"Dismissed — {rule_display}"
    elif params.estimated_savings > 0:
        headline = f"{format_savings(params.estimated_savings)}/mo recovered — {rule_display}"
    else:
        headline = f"Resolved — {rule_display}"

    blocks = [
        header_block(headline),
        {"type": "context", "elements": [{"type": "mrkdwn", "text": f"{branding} · recommendation resolved"}]},
    ]

    lines = [f"*{params.resource_name}*"]
    resolution_line = _resolution_line(params)
    if resolution_line:
        lines.append(resolution_line)
    if params.account_name:
        lines.append(f"Acct: {params.account_name}")

    cta_url = f"{base_url}/optimise?id={params.recommendation_id}#resolutions"
    attachment = legacy_attachment(
        STRIPE_SAVINGS,
        headline,
        text="\n".join(lines),
        actions=[
            link_button("View Details", cta_url, style="primary"),
            link_button("View All Recommendations", f"{base_url}/optimise?utm=slack#recommendations"),
        ],
    )

    return {
        "text": headline,
        "blocks": blocks[:50],
        "attachments": [attachment],
        "unfurl_links": False,
    }
