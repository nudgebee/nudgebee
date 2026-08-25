"""Discord renderer for the weekly digest.

Mirrors message_templates/google_chat/weekly_digest_events.py, shaped into
the {"content", "embeds"} payload DiscordClient.chat_post consumes. clamp_embeds
enforces Discord's limits, so a long lede or an unusually wordy finding cannot
produce an undeliverable message.
"""

from typing import Any, Dict, List

from notifications_server.configs.settings import settings
from notifications_server.message_templates.discord.embed_utils import clamp_embeds
from notifications_server.message_templates.slack.weekly_digest_events import (
    WeeklyDigestParams,
    finding_facts_line,
    finding_title,
    digest_headline,
    digest_link,
    scoreboard_pairs,
)

# Slate — the review is a status document, not an alert, so it deliberately does
# not borrow the FinOps digest's green or an alert red.
REVIEW_COLOR = 9807270  # #95A5A6


def _summary_lines(params: WeeklyDigestParams) -> List[str]:
    lines = [f"**{label}:** {value}" for label, value in scoreboard_pairs(params)]
    if params.lede:
        lines.append("")
        lines.append(params.lede)
    return lines


def get_discord_weekly_digest_events_template(params: WeeklyDigestParams) -> Dict[str, Any]:
    subtitle = f"{settings.urls.branding_name} weekly digest"
    if params.period_label:
        subtitle += f" · {params.period_label}"

    embeds: List[Dict[str, Any]] = [
        {
            "title": digest_headline(params),
            "description": "\n".join([subtitle, ""] + _summary_lines(params)),
            "color": REVIEW_COLOR,
        }
    ]

    if params.top_findings:
        finding_lines: List[str] = []
        for f in params.top_findings:
            finding_lines.append(f"**{finding_title(f)}**")
            if f.headline:
                finding_lines.append(f.headline)
            facts = finding_facts_line(f)
            if facts:
                finding_lines.append(f"_{facts}_")
            finding_lines.append("")
        embeds.append(
            {
                "title": "What broke",
                "description": "\n".join(finding_lines).strip(),
                "color": REVIEW_COLOR,
            }
        )

    footer_bits = []
    if params.more_findings > 0:
        footer_bits.append(f"_+{params.more_findings} more in the full review_")
    footer_bits.append(f"[View Full Review]({digest_link(params)})")

    last = embeds[-1]
    last["description"] = (last.get("description", "") + "\n\n" + "\n".join(footer_bits)).strip()

    return {"content": f"{params.title} · {digest_headline(params)}", "embeds": clamp_embeds(embeds)}
