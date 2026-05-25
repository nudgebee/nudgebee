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

    # Summary: recoverable + deltas. Recoverable hidden when 0 (security-only
    # digests). NEW/resolved/carryover render only when their data is present
    # so old-payload renders gracefully degrade.
    if params.total_recoverable_savings > 0:
        lines.append(f"*{format_savings(params.total_recoverable_savings)}/mo recoverable*")

    if params.new_counts is not None:
        new_parts: List[str] = []
        if params.new_counts.act_now:
            new_parts.append(f"{params.new_counts.act_now} Priority")
        if params.new_counts.critical:
            new_parts.append(f"{params.new_counts.critical} Critical")
        if params.new_counts.high:
            new_parts.append(f"{params.new_counts.high} High")
        if new_parts:
            lines.append("*New since yesterday:* " + " \u00b7 ".join(new_parts))
        else:
            lines.append("_No new recommendations since yesterday._")

    status_bits: List[str] = []
    if params.resolved_count:
        suffix = f" (saved {format_savings(params.resolved_savings)}/mo)" if params.resolved_savings > 0 else ""
        status_bits.append(f"*{params.resolved_count} resolved*{suffix}")
    if params.carryover_count:
        status_bits.append(f"{params.carryover_count} still open from earlier")
    if status_bits:
        lines.append(" \u00b7 ".join(status_bits))

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

    footer_url = f"{base_url}/optimise?utm=gchat-digest"
    if params.digest_date:
        footer_url += f"&d={params.digest_date}"
    footer_url += "#recommendations"
    lines.append(f"View all recommendations: {footer_url}")

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
