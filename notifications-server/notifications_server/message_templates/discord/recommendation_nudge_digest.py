"""Discord renderer for the FinOps recommendation nudge digest.

Mirrors message_templates/google_chat/recommendation_nudge_digest.py: a summary
of recoverable savings and deltas, then top recommendations per FinOps band
(one embed per band). Returns the {"content", "embeds"} payload consumed by
DiscordClient.chat_post.
"""

from typing import Any, Dict, List, Tuple

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.discord.embed_utils import clamp_embeds
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    BAND_DISPLAY_NAMES,
    BAND_ORDER,
    DigestRecommendation,
    RecommendationNudgeDigestParams,
    collect_recs_by_band,
    format_rule_name,
    format_savings,
)

DIGEST_COLOR = 3066993  # green (#2ecc71)


def _summary_lines(params: RecommendationNudgeDigestParams) -> List[str]:
    lines: List[str] = []

    if params.total_recoverable_savings > 0:
        lines.append(f"**{format_savings(params.total_recoverable_savings)}/mo recoverable**")

    if params.new_counts is not None:
        new_parts: List[str] = []
        if params.new_counts.act_now:
            new_parts.append(f"{params.new_counts.act_now} Priority")
        if params.new_counts.critical:
            new_parts.append(f"{params.new_counts.critical} Critical")
        if params.new_counts.high:
            new_parts.append(f"{params.new_counts.high} High")
        if new_parts:
            lines.append("**New since yesterday:** " + " · ".join(new_parts))
        else:
            lines.append("*No new recommendations since yesterday.*")

    status_bits: List[str] = []
    if params.resolved_count:
        suffix = f" (saved {format_savings(params.resolved_savings)}/mo)" if params.resolved_savings > 0 else ""
        status_bits.append(f"**{params.resolved_count} resolved**{suffix}")
    if params.carryover_count:
        status_bits.append(f"{params.carryover_count} still open from earlier")
    if status_bits:
        lines.append(" · ".join(status_bits))

    return lines


def _band_lines(band_recs: List[Tuple[str, DigestRecommendation]]) -> List[str]:
    lines: List[str] = []
    for _account_name, rec in band_recs[:5]:
        savings_text = f" — {format_savings(rec.estimated_savings)}/mo" if rec.estimated_savings > 0 else ""
        review_link = f"  [Review]({rec.cta_url})" if rec.cta_url else ""
        lines.append(f"• **{rec.resource_name}** {format_rule_name(rec.rule_name)}{savings_text}{review_link}")

    remaining = len(band_recs) - 5
    if remaining > 0:
        lines.append(f"*and {remaining} more...*")
    return lines


def get_discord_recommendation_nudge_digest_template(params: RecommendationNudgeDigestParams) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()

    header_embed: Dict[str, Any] = {
        "title": f"{settings.urls.branding_name} {params.title}",
        "color": DIGEST_COLOR,
    }
    summary = _summary_lines(params)
    if summary:
        header_embed["description"] = "\n".join(summary)
    embeds: List[Dict[str, Any]] = [header_embed]

    recs_by_band = collect_recs_by_band(params)
    for band in BAND_ORDER:
        band_recs = recs_by_band[band]
        if not band_recs:
            continue
        embeds.append(
            {
                "title": BAND_DISPLAY_NAMES.get(band, band),
                "description": "\n".join(_band_lines(band_recs)),
                "color": DIGEST_COLOR,
            }
        )

    # utm=discord-digest distinguishes digest clicks; digest_date lets analytics
    # correlate clicks back to a specific brief (same scheme as Slack).
    footer_url = f"{base_url}/optimise?utm=discord-digest"
    if params.digest_date:
        footer_url += f"&d={params.digest_date}"
    footer_url += "#recommendations"
    last = embeds[-1]
    last["description"] = (last.get("description", "") + f"\n\n[View All Recommendations]({footer_url})").strip()

    content = f"{params.title} - {format_savings(params.total_recoverable_savings)}/mo recoverable"
    return {"content": content, "embeds": clamp_embeds(embeds)}
