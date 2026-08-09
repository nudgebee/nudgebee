"""Google Chat renderer for the weekly digest.

Google Chat takes plain text with light markup, so this is the Slack layout
flattened to lines. Same content and same ordering — see
message_templates/slack/weekly_digest_events.py for why it is a summary
rather than the review itself.
"""

from typing import Any, Dict, List

from notifications_server.configs.settings import settings
from notifications_server.message_templates.slack.weekly_digest_events import (
    WeeklyDigestParams,
    finding_facts_line,
    finding_title,
    digest_headline,
    digest_link,
    scoreboard_pairs,
)


def get_gchat_weekly_digest_events_template(params: WeeklyDigestParams) -> Dict[str, Any]:
    lines: List[str] = [f"*{digest_headline(params)}*"]

    subtitle = f"{settings.urls.branding_name} weekly digest"
    if params.period_label:
        subtitle += f" · {params.period_label}"
    lines.append(subtitle)
    lines.append("-" * 25)

    lines.append(" · ".join(f"*{label}:* {value}" for label, value in scoreboard_pairs(params)))

    if params.lede:
        lines.append("")
        lines.append(params.lede)

    if params.top_findings:
        lines.append("")
        for f in params.top_findings:
            lines.append(f"  • *{finding_title(f)}*")
            if f.headline:
                lines.append(f"    {f.headline}")
            facts = finding_facts_line(f)
            if facts:
                lines.append(f"    _{facts}_")

    if params.more_findings > 0:
        lines.append(f"  _and {params.more_findings} more..._")

    lines.append("")
    lines.append(f"View full review: {digest_link(params)}")

    return {"text": "\n".join(lines)}
