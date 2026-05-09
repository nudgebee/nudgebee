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


def get_gchat_recommendation_nudge_digest_template(
    params: RecommendationNudgeDigestParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    lines: List[str] = []

    lines.append(f"*{settings.urls.branding_name} {params.title}*")
    lines.append("-" * 25)

    # Summary line
    summary_parts = [f"*{format_savings(params.total_recoverable_savings)}/mo recoverable*"]
    if params.act_now_count > 0:
        summary_parts.append(f"{params.act_now_count} Priority")
    if params.critical_count > 0:
        summary_parts.append(f"{params.critical_count} Critical")
    if params.high_count > 0:
        summary_parts.append(f"{params.high_count} High")
    lines.append(" \u00b7 ".join(summary_parts))
    lines.append("")

    # Recommendations grouped by band
    recs_by_band = collect_recs_by_band(params)
    for band in BAND_ORDER:
        band_recs = recs_by_band[band]
        if not band_recs:
            continue
        display_name = BAND_DISPLAY_NAMES.get(band, band)
        lines.append(f"*{display_name}*")
        _append_gchat_rec_lines(lines, band_recs)
        lines.append("")

    lines.append(f"View all recommendations: {base_url}/optimize/summary?utm=gchat")

    return {"text": "\n".join(lines)}


def _append_gchat_rec_lines(
    lines: List[str],
    band_recs: List[Tuple[str, DigestRecommendation]],
) -> None:
    """Append up to 5 recommendation lines plus overflow."""
    for _account_name, rec in band_recs[:5]:
        savings_text = f" \u2014 {format_savings(rec.estimated_savings)}/mo" if rec.estimated_savings > 0 else ""
        lines.append(f"  \u2022 *{rec.resource_name}* {format_rule_name(rec.rule_name)}{savings_text}")

    remaining = len(band_recs) - 5
    if remaining > 0:
        lines.append(f"  _and {remaining} more..._")
