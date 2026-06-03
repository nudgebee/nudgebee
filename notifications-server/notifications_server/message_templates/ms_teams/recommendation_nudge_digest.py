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

    # Summary FactSet: recoverable savings + delta counts. Recoverable is
    # omitted when 0 so security-only digests don't lead with '$0/mo'. The
    # legacy act_now/critical/high totals are replaced by the NEW-since-yesterday
    # counts and resolved/carryover, which are the actually-useful signals.
    facts: List[Dict[str, str]] = []
    if params.total_recoverable_savings > 0:
        facts.append({"title": "Recoverable", "value": f"{format_savings(params.total_recoverable_savings)}/mo"})

    if params.new_counts is not None:
        new_parts: List[str] = []
        if params.new_counts.act_now:
            new_parts.append(f"{params.new_counts.act_now} Priority")
        if params.new_counts.critical:
            new_parts.append(f"{params.new_counts.critical} Critical")
        if params.new_counts.high:
            new_parts.append(f"{params.new_counts.high} High")
        facts.append({"title": "New since yesterday", "value": " · ".join(new_parts) if new_parts else "None"})

    if params.resolved_count:
        suffix = f" (saved {format_savings(params.resolved_savings)}/mo)" if params.resolved_savings > 0 else ""
        facts.append({"title": "Resolved", "value": f"{params.resolved_count}{suffix}"})

    if params.carryover_count:
        facts.append({"title": "Still open from earlier", "value": str(params.carryover_count)})

    if facts:
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

    footer_url = f"{base_url}/optimise?utm=teams-digest"
    if params.digest_date:
        footer_url += f"&d={params.digest_date}"
    footer_url += "#recommendations"
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
                "url": footer_url,
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
