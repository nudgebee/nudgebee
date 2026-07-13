"""Discord renderer for recommendation resolution notifications.

Mirrors message_templates/google_chat/recommendation_resolution.py: the
resolved recommendation summary plus resolution details, with the Slack
template's deep links. Returns the {"content", "embeds"} payload consumed by
DiscordClient.chat_post.
"""

from typing import Any, Dict, List

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.discord.embed_utils import _v, clamp_embeds
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    format_rule_name,
    format_savings,
)
from notifications_server.message_templates.slack.recommendation_resolution import (
    BAND_DISPLAY_NAMES,
    RecommendationResolutionParams,
)

RESOLUTION_COLOR = 3066993  # green (#2ecc71)


def get_discord_recommendation_resolution_template(params: RecommendationResolutionParams) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    branding = settings.urls.branding_name
    band_display = BAND_DISPLAY_NAMES.get(params.finops_band, params.finops_band)

    cta_url = f"{base_url}/optimise?id={params.recommendation_id}#resolutions"
    view_all_url = f"{base_url}/optimise?utm=discord#recommendations"
    description_lines = [
        f"**{params.resource_name}**",
        f"{format_rule_name(params.rule_name)} · {params.account_name}",
        (
            f"{band_display} · Score: {params.finops_score}/100 · "
            f"Savings: {format_savings(params.estimated_savings)}/mo"
        ),
        "",
        f"[View Details]({cta_url}) · [View All Recommendations]({view_all_url})",
    ]

    fields: List[Dict[str, Any]] = [{"name": "Status", "value": _v(params.status), "inline": True}]
    if params.resolution:
        res = params.resolution
        if res.resolver:
            fields.append({"name": "Resolver", "value": res.resolver, "inline": True})
        if res.type:
            fields.append({"name": "Type", "value": res.type, "inline": True})
        if res.status:
            fields.append({"name": "Resolution Status", "value": res.status, "inline": True})
        if res.status_message:
            fields.append({"name": "Message", "value": res.status_message, "inline": False})

    embed = {
        "title": f"{branding} Recommendation Resolved",
        "description": "\n".join(description_lines),
        "color": RESOLUTION_COLOR,
        "fields": fields,
    }
    content = f"✅ Resolved: {params.resource_name} — {format_rule_name(params.rule_name)}"
    return {"content": content, "embeds": clamp_embeds([embed])}
