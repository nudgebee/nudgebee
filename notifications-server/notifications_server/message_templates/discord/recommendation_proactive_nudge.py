"""Discord renderer for the proactive FinOps nudge (priority recommendations).

Mirrors message_templates/google_chat/recommendation_proactive_nudge.py: an
alert summary plus top recommendations per account, with the shared Ask-Nubi
deep link. Returns the {"content", "embeds"} payload consumed by
DiscordClient.chat_post.
"""

from typing import Any, Dict, List

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.discord.embed_utils import clamp_embeds
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    format_rule_name,
    format_savings,
)
from notifications_server.message_templates.slack.recommendation_proactive_nudge import (
    ProactiveNudgeParams,
    build_ask_nubi_url,
)

PROACTIVE_NUDGE_COLOR = 16750848  # orange (#ff9800)


def get_discord_recommendation_proactive_nudge_template(params: ProactiveNudgeParams) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    branding = settings.urls.branding_name

    embeds: List[Dict[str, Any]] = [
        {
            "title": f"{branding} FinOps Alert — Priority",
            "description": (
                f"{params.total_recommendations} recommendations require immediate action\n"
                f"Total recoverable: **{format_savings(params.total_recoverable_savings)}/mo**"
            ),
            "color": PROACTIVE_NUDGE_COLOR,
        }
    ]

    counter = 1
    for _acc_id, acc_data in params.recommendations_by_account.items():
        lines: List[str] = []
        for rec in acc_data.recommendations[:5]:
            lines.append(f"{counter}. **{rec.resource_name}** — {format_rule_name(rec.rule_name)}")
            lines.append(
                f"Savings: {format_savings(rec.estimated_savings)}/mo · "
                f"Severity: {rec.severity} · Category: {rec.category}"
            )
            counter += 1

        remaining = len(acc_data.recommendations) - 5
        if remaining > 0:
            lines.append(f"*and {remaining} more...*")

        embeds.append(
            {
                "title": acc_data.account_name,
                "description": "\n".join(lines),
                "color": PROACTIVE_NUDGE_COLOR,
            }
        )

    links = (
        f"[View All Recommendations]({base_url}/optimise?utm=discord#recommendations) · "
        f"[Ask Nubi]({build_ask_nubi_url(params, base_url, 'discord')})"
    )
    last = embeds[-1]
    last["description"] = (last.get("description", "") + f"\n\n{links}").strip()

    content = (
        f"Priority: {params.total_recommendations} recommendations — "
        f"{format_savings(params.total_recoverable_savings)}/mo recoverable"
    )
    return {"content": content, "embeds": clamp_embeds(embeds)}
