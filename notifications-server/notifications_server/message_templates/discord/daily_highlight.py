"""Discord renderer for the daily highlight (daily recap) report.

Mirrors message_templates/google_chat/daily_highlight.py: an org-wide savings
header, then one embed per cluster with insights, recommendations, and event
highlights. Returns the {"content", "embeds"} payload consumed by
DiscordClient.chat_post.
"""

from typing import Any, Dict, List

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.discord.embed_utils import clamp_embeds
from notifications_server.message_templates.slack.daily_highlight import (
    DailyRecapParams,
    Insight,
    Recommendation,
    calculate_total_savings,
    calculate_trend,
    categorize_insights,
    format_rule_name,
    group_events_by_account,
    group_insights_by_account,
    group_recommendations_by_account,
)

DAILY_HIGHLIGHT_COLOR = 3581519  # green (#36a64f), same as the Slack attachment color


def _format_insights(insights: List[Insight]) -> List[str]:
    lines: List[str] = []
    categorized = categorize_insights(insights)
    if any(categorized.values()):
        lines.append("🔍 **Insights:**")
        for items in categorized.values():
            for insight in items:
                app_count = len(insight.applications) if insight.applications else 0
                if app_count > 0:
                    lines.append(f"• {insight.title} detected for ({app_count}) workloads")
                else:
                    lines.append(f"• {insight.title}")
    return lines


def _format_recommendations(recs: List[Recommendation]) -> List[str]:
    if not recs:
        return []
    lines = ["💡 **Recommendations:**"]
    for rec in sorted(recs, key=lambda x: x.sum_estimated_savings, reverse=True)[:5]:
        if rec.sum_estimated_savings > 0:
            lines.append(
                f"• **{rec.count}** {format_rule_name(rec.rule_name)} – "
                f"potential savings: **${rec.sum_estimated_savings:.2f}**/month"
            )
        else:
            lines.append(f"• **{rec.count}** {format_rule_name(rec.rule_name)} available")
    return lines


def _format_highlights(current: Dict[str, int], previous: Dict[str, int]) -> List[str]:
    if sum(current.values()) == 0:
        return []
    lines = ["✨ **Highlights:**"]
    event_texts = []
    for etype, label in [("app", "Application Errors"), ("node", "Node Errors"), ("pod", "Pod Errors")]:
        count = current.get(etype, 0)
        if count > 0:
            trend = calculate_trend(count, previous.get(etype, 0))
            event_texts.append(f"{label}: {count} {trend}")
    if event_texts:
        lines.append("• " + ", ".join(event_texts))
    return lines


def get_discord_daily_highlight_template(params: DailyRecapParams) -> Dict[str, Any]:
    header_lines: List[str] = []

    total_savings = 0.0
    if params.highlight1 and params.highlight1.data.recommendation_groupings.rows:
        total_savings = calculate_total_savings(params.highlight1.data.recommendation_groupings.rows)
    if total_savings > 0:
        header_lines.append(f"**Potential Savings:** ${total_savings:.2f}/month")

    total_opportunity_lost = params.total_opportunity_lost or 0
    if total_opportunity_lost > 0:
        header_lines.append(f"**Opportunity Lost (30 days):** ${total_opportunity_lost:.2f}")

    header_lines.append(
        "Here's your daily recap for all your clusters. See how clusters are performing and opportunities to optimize!"
    )
    header_lines.append(f"[View full report]({public_ip()}/home)")

    embeds: List[Dict[str, Any]] = [
        {
            "title": f"{params.title} - {params.organization_name}",
            "description": "\n".join(header_lines),
            "color": DAILY_HIGHLIGHT_COLOR,
        }
    ]
    content = f"📊 {params.title} - {params.organization_name}"

    if not (params.highlight1 and params.insights):
        return {"content": content, "embeds": clamp_embeds(embeds)}

    current_data = params.highlight1.data
    previous_data = params.highlight2.data if params.highlight2 else None
    all_insights = params.insights.data.insight or []
    account_name_map = {a.id: a.account_name for a in params.insights.data.cloud_accounts}

    current_app_events = group_events_by_account(current_data.app_events.rows)
    current_node_events = group_events_by_account(current_data.node_events.rows)
    current_pod_events = group_events_by_account(current_data.pod_events.rows)

    previous_app_events = group_events_by_account(previous_data.app_events.rows) if previous_data else {}
    previous_node_events = group_events_by_account(previous_data.node_events.rows) if previous_data else {}
    previous_pod_events = group_events_by_account(previous_data.pod_events.rows) if previous_data else {}

    recs_by_account = group_recommendations_by_account(current_data.recommendation_groupings.rows)
    insights_by_account = group_insights_by_account(all_insights)

    unique_accounts = set(insights_by_account.keys()) | set(recs_by_account.keys())
    for account_id in unique_accounts:
        account_name = account_name_map.get(account_id, "")
        if not account_name:
            continue
        lines: List[str] = []
        lines.extend(_format_insights(insights_by_account.get(account_id, [])))
        lines.extend(_format_recommendations(recs_by_account.get(account_id, [])))
        current_events = {
            "app": current_app_events.get(account_id, 0),
            "node": current_node_events.get(account_id, 0),
            "pod": current_pod_events.get(account_id, 0),
        }
        prev_events = {
            "app": previous_app_events.get(account_id, 0),
            "node": previous_node_events.get(account_id, 0),
            "pod": previous_pod_events.get(account_id, 0),
        }
        lines.extend(_format_highlights(current_events, prev_events))
        if not lines:
            continue
        embeds.append(
            {
                "title": f"Cluster: {account_name}",
                "url": f"{public_ip()}/kubernetes/details/{account_id}",
                "description": "\n".join(lines),
                "color": DAILY_HIGHLIGHT_COLOR,
            }
        )

    return {"content": content, "embeds": clamp_embeds(embeds)}
