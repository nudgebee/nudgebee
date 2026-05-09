from typing import Dict, Any, List, Tuple

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    BAND_DISPLAY_NAMES,
    BAND_ORDER,
    DigestRecommendation,
    RecommendationNudgeDigestParams,
    collect_recs_by_band,
    format_rule_name,
    format_savings,
)


def get_teams_recommendation_nudge_digest_template(
    params: RecommendationNudgeDigestParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    body: List[Dict[str, Any]] = []

    # Header
    body.append(
        {
            "type": "TextBlock",
            "text": f"{settings.urls.branding_name} {params.title}",
            "size": "Large",
            "weight": "Bolder",
            "wrap": True,
        }
    )

    # Summary facts
    facts = [
        {
            "title": "Recoverable",
            "value": f"{format_savings(params.total_recoverable_savings)}/mo",
        },
    ]
    if params.act_now_count > 0:
        facts.append({"title": "Priority", "value": str(params.act_now_count)})
    if params.critical_count > 0:
        facts.append({"title": "Critical", "value": str(params.critical_count)})
    if params.high_count > 0:
        facts.append({"title": "High", "value": str(params.high_count)})

    body.append({"type": "FactSet", "facts": facts})
    body.append({"type": "TextBlock", "text": "---", "separator": True})

    # Recommendations grouped by band
    recs_by_band = collect_recs_by_band(params)
    for band in BAND_ORDER:
        band_recs = recs_by_band[band]
        if not band_recs:
            continue
        display_name = BAND_DISPLAY_NAMES.get(band, band)
        body.append(
            {
                "type": "TextBlock",
                "text": f"**{display_name}**",
                "weight": "Bolder",
                "wrap": True,
            }
        )
        _append_teams_rec_blocks(body, band_recs)

    return {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.2",
        "msteams": {"width": "Full"},
        "body": body,
        "actions": [
            {
                "type": "Action.OpenUrl",
                "title": "View All Recommendations",
                "url": f"{base_url}/optimize/summary?utm=teams",
            }
        ],
    }


def _append_teams_rec_blocks(
    body: List[Dict[str, Any]],
    band_recs: List[Tuple[str, DigestRecommendation]],
) -> None:
    """Append up to 5 recommendation text blocks plus overflow."""
    for _account_name, rec in band_recs[:5]:
        savings_text = f" — {format_savings(rec.estimated_savings)}/mo" if rec.estimated_savings > 0 else ""
        body.append(
            {
                "type": "TextBlock",
                "text": f"• **{rec.resource_name}** {format_rule_name(rec.rule_name)}{savings_text}",
                "wrap": True,
            }
        )

    remaining = len(band_recs) - 5
    if remaining > 0:
        body.append(
            {
                "type": "TextBlock",
                "text": f"_and {remaining} more..._",
                "isSubtle": True,
                "wrap": True,
            }
        )
