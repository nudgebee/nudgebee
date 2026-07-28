"""Discord renderer for the events summary report.

Mirrors message_templates/google_chat/event_summary.py: per-account event
counts by severity/type plus top events, one embed per account. Returns the
{"content", "embeds"} payload consumed by DiscordClient.chat_post.
"""

from typing import Any, Dict, List

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.discord.embed_utils import clamp_embeds
from notifications_server.message_templates.slack.events_summary import (
    EventCountRow,
    EventsSummaryPayload,
    EventSummarisedRow,
)

EVENTS_SUMMARY_COLOR = 2201331  # blue (#2196f3), same as the Slack attachment color


def _account_lines(counts: EventCountRow, account_id: str, events_summarised: List[EventSummarisedRow]) -> List[str]:
    lines = [
        f"**Total Events:** {counts.event_count}",
        (
            f"**High:** {counts.count_priority_high}, **Medium:** {counts.count_priority_medium}, "
            f"**Low:** {counts.count_priority_low}"
        ),
        (
            f"**Types** - **App Issues:** {counts.count_application_issues}, "
            f"**Node Issues:** {counts.count_node_issues}, **Pod Issues:** {counts.count_pod_issues}"
        ),
    ]
    dedup: Dict[str, int] = {}
    for event in events_summarised:
        if event.account_id != account_id or event.event_count <= 0:
            continue
        dedup[event.aggregation_key] = dedup.get(event.aggregation_key, 0) + event.event_count
    if dedup:
        lines.append("**Top Events:**")
        for key, count in sorted(dedup.items(), key=lambda x: x[1], reverse=True)[:5]:
            lines.append(f"• {key}: {count}")
    return lines


def get_discord_events_summary_template(payload: EventsSummaryPayload) -> Dict[str, Any]:
    event_counts = payload.event_counts.data.event_groupings.rows
    events = payload.events_summarised.data.event_groupings.rows

    embeds: List[Dict[str, Any]] = []
    for acct in payload.accounts.data.accounts:
        counts = next((row for row in event_counts if row.account_id == acct.id), None)
        if not counts or counts.event_count <= 0:
            continue
        embeds.append(
            {
                "title": f"Account: {acct.account_name}",
                "url": f"{public_ip()}/kubernetes/details/{acct.id}",
                "description": "\n".join(_account_lines(counts, acct.id, events)),
                "color": EVENTS_SUMMARY_COLOR,
            }
        )

    content = f"⚙️ {settings.urls.branding_name} Events Summary - {payload.organization_name}"
    return {"content": content, "embeds": clamp_embeds(embeds)}
