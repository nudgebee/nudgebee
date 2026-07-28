"""Discord renderer for Kubernetes recommendation notifications.

Mirrors message_templates/google_chat/recommendation.py (top-N recommendations,
security grouped by severity) using the Slack template's deep-link URL
patterns. Returns the {"content", "embeds"} payload consumed by
DiscordClient.chat_post.
"""

from typing import Any, Dict, List, Optional

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.discord.embed_utils import clamp_embeds
from notifications_server.message_templates.slack.recommendation import (
    RecommendationParams,
    RecommendationSummary,
    RecommendationTabs,
)

RECOMMENDATION_COLOR = 3066993  # green (#2ecc71)
MAX_ITEMS = 5


def _format_totals(new: Optional[int], resolved: Optional[int]) -> str:
    parts = []
    if new:
        parts.append(f"{new} new")
    if resolved:
        parts.append(f"{resolved} resolved")
    return " and ".join(parts) if parts else "0 recommendations"


def _format_single_rec(rec: RecommendationSummary, idx: int) -> List[str]:
    if not rec.summary:
        return []

    chunk: List[str] = [f"{idx}. **{rec.summary}**"]
    if rec.savings and rec.savings > 0:
        chunk.append(f"- **Savings:** ${rec.savings}/month")
    if rec.severity:
        chunk.append(f"- **Severity:** {rec.severity}")
    chunk.append(f"- **Status:** {rec.status}")

    if rec.resolution:
        res = rec.resolution
        chunk.extend(
            [
                f"- **Resolver:** {res.resolver}",
                f"- **Type:** {res.type}",
                f"- **Reference:** {res.type_reference}",
                f"- **Resolution Status:** {res.status}",
                f"- **Message:** {res.status_message}",
            ]
        )
    return chunk


def _append_grouped_recs(lines: List[str], recommendations: List[RecommendationSummary], max_items: int) -> None:
    severity_order = {"critical": 1, "high": 2}
    groups: Dict[str, List[RecommendationSummary]] = {}
    for rec in recommendations[:max_items]:
        key = rec.severity.capitalize() if rec.severity else "Other"
        groups.setdefault(key, []).append(rec)
    sorted_keys = sorted(groups.keys(), key=lambda k: severity_order.get(k.lower(), 99))

    counter = 1
    for key in sorted_keys:
        lines.append(f"**{key} Severity:**")
        for rec in groups[key]:
            lines.extend(_format_single_rec(rec, counter))
            counter += 1


def get_discord_recommendation_template(params: RecommendationParams) -> Dict[str, Any]:
    tab = RecommendationTabs.get_value(params.category.upper())
    account_url = f"{public_ip()}/kubernetes/details/{params.account_id}?utm=discord"
    view_all_url = (
        f"{public_ip()}/kubernetes/details/{params.account_id}?accountId={params.account_id}&utm=discord#{tab}"
    )

    header_lines = [f"**Account:** [{params.account_name}]({account_url})"]
    if params.total_estimated_savings:
        header_lines.append(f"**Total Estimated Savings:** ${params.total_estimated_savings}/month")
    if params.rule_description:
        header_lines.append(f"**Rule Description:** {params.rule_description}")

    totals_text = _format_totals(params.total_new, params.total_archived)
    affected_suffix = (
        f" **{params.total_affected_resources} affected resources.**" if params.total_affected_resources else ""
    )
    header_lines.append(f"We found **{totals_text}**.{affected_suffix} [View all recommendations]({view_all_url})")

    embeds: List[Dict[str, Any]] = [
        {
            "title": f"Kubernetes {params.category} Recommendations",
            "description": "\n".join(header_lines),
            "color": RECOMMENDATION_COLOR,
        }
    ]

    filtered = [rec for rec in params.recommendations if rec.summary is not None]
    if filtered:
        rec_lines: List[str] = []
        if params.category.lower() == "security":
            _append_grouped_recs(rec_lines, filtered, MAX_ITEMS)
        else:
            for idx, rec in enumerate(filtered[:MAX_ITEMS], start=1):
                rec_lines.extend(_format_single_rec(rec, idx))

        remaining = len(filtered) - MAX_ITEMS
        if remaining > 0:
            rec_lines.append(f"…and **{remaining} more** recommendations. [See all]({view_all_url})")

        embeds.append(
            {
                "title": f"Top {min(len(filtered), MAX_ITEMS)} Recommendations",
                "description": "\n".join(rec_lines),
                "color": RECOMMENDATION_COLOR,
            }
        )

    embeds[-1]["description"] += f"\n\nView more details on {settings.urls.branding_link('markdown')}"

    content = f"🚀 Kubernetes {params.category} Recommendations for {params.account_name}"
    return {"content": content, "embeds": clamp_embeds(embeds)}
