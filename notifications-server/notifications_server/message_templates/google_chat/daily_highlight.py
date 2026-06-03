from typing import Dict, Any, List
from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.daily_highlight import (
    calculate_trend,
    group_events_by_account,
    group_recommendations_by_account,
    group_insights_by_account,
    calculate_total_savings,
    DailyRecapParams,
    categorize_insights,
    format_rule_name,
    Recommendation,
    Insight,
)


def format_insights(insights: List[Insight]) -> List[str]:
    lines = []
    categorized = categorize_insights(insights)
    if any(categorized.values()):
        lines.append("🔍 *Insights:*")
        for items in categorized.values():
            for insight in items:
                app_count = len(insight.applications) if insight.applications else 0
                if app_count > 0:
                    lines.append(f"• {insight.title} detected for ({app_count}) workloads")
                else:
                    lines.append(f"• {insight.title}")
    return lines


def format_recommendations(recs: List[Recommendation]) -> List[str]:
    if not recs:
        return []
    lines = ["💡 *Recommendations:*"]
    for rec in sorted(recs, key=lambda x: x.sum_estimated_savings, reverse=True)[:5]:
        if rec.sum_estimated_savings > 0:
            lines.append(
                f"• *{rec.count}* {format_rule_name(rec.rule_name)} – "
                f"potential savings: *${rec.sum_estimated_savings:.2f}*/month 🚀"
            )
        else:
            lines.append(f"• *{rec.count}* {format_rule_name(rec.rule_name)} available 🚀")
    return lines


def format_highlights(current: Dict[str, int], previous: Dict[str, int]) -> List[str]:
    if sum(current.values()) == 0:
        return []
    lines = ["✨ *Highlights:*"]
    event_texts = []
    for etype, label in [("app", "Application Errors"), ("node", "Node Errors"), ("pod", "Pod Errors")]:
        count = current.get(etype, 0)
        if count > 0:
            trend = calculate_trend(count, previous.get(etype, 0))
            event_texts.append(f"{label}: {count} {trend}")
    if event_texts:
        lines.append("• " + ", ".join(event_texts))
    return lines


def get_gchat_daily_highlight_template(params: DailyRecapParams) -> Dict[str, Any]:
    def build_account_section(act_id: str) -> List[str]:
        account_name = account_name_map.get(act_id, "")
        if not account_name:
            return [""]
        lines_ = [f"☁️ *Cluster*: {account_name}"]
        lines_.extend(format_insights(insights_by_account.get(act_id, [])))
        lines_.extend(format_recommendations(recs_by_account.get(act_id, [])))
        current_events = {
            "app": current_app_events.get(act_id, 0),
            "node": current_node_events.get(act_id, 0),
            "pod": current_pod_events.get(act_id, 0),
        }
        prev_events = {
            "app": previous_app_events.get(act_id, 0),
            "node": previous_node_events.get(act_id, 0),
            "pod": previous_pod_events.get(act_id, 0),
        }
        lines_.extend(format_highlights(current_events, prev_events))
        lines_.append("-" * 25)
        return lines_

    # === Header ===
    lines: List[str] = [f"*{params.title} - {params.organization_name}*"]

    total_savings = 0
    if params.highlight1 and params.highlight1.data.recommendation_groupings.rows:
        total_savings = calculate_total_savings(params.highlight1.data.recommendation_groupings.rows)
    if total_savings > 0:
        lines.append(f"*Potential Savings*: ${total_savings:.2f}/month")

    total_opportunity_lost = params.total_opportunity_lost or 0
    if total_opportunity_lost > 0:
        lines.append(f"*Opportunity Lost (30 days)*: ${total_opportunity_lost:.2f}")

    lines.append(
        "Here's your daily recap for all your clusters. See how clusters are performing and opportunities to optimize!"
    )
    lines.append("-" * 25)

    # === Data Prep ===
    if not (params.highlight1 and params.insights):
        return {"text": "\n".join(lines)}

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

    # === Build Sections ===
    unique_accounts = set(insights_by_account.keys()) | set(recs_by_account.keys())
    for account_id in unique_accounts:
        lines.extend(build_account_section(account_id))

    # === Footer ===
    view_all_url = f"{public_ip()}/home"
    lines.append(f"View full report: {view_all_url}")

    return {"text": "\n".join(lines).strip()}
