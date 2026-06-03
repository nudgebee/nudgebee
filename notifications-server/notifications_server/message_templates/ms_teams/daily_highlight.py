from typing import Dict, Any, List
from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.base import Emojis
from notifications_server.message_templates.slack.daily_highlight import (
    DailyRecapParams,
    Insight,
    categorize_insights,
    Recommendation,
    format_rule_name,
    calculate_trend,
    group_events_by_account,
    group_recommendations_by_account,
    group_insights_by_account,
    calculate_total_savings,
)


def _format_insights(insights: List[Insight]) -> List[Dict[str, Any]]:
    if not insights:
        return []
    items = [{"type": "TextBlock", "text": f"{Emojis.Investigate.value} Insights", "weight": "Bolder"}]
    categorized = categorize_insights(insights)
    for category, cat_insights in categorized.items():
        for insight in cat_insights:
            app_count = len(insight.applications) if insight.applications else 0
            text = f"• {insight.title}"
            if app_count > 0:
                text += f" ({app_count} workloads)"
            items.append({"type": "TextBlock", "text": text, "wrap": True})
    return items


def _format_recommendations(recs: List[Recommendation]) -> List[Dict[str, Any]]:
    if not recs:
        return []
    items = [{"type": "TextBlock", "text": f"{Emojis.New.value} Recommendations", "weight": "Bolder"}]
    for rec in sorted(recs, key=lambda x: x.sum_estimated_savings, reverse=True)[:5]:
        if rec.sum_estimated_savings > 0:
            text = f"• {rec.count} {format_rule_name(rec.rule_name)} – savings: ${rec.sum_estimated_savings:.2f}/month"
        else:
            text = f"• {rec.count} {format_rule_name(rec.rule_name)}"
        items.append({"type": "TextBlock", "text": text, "wrap": True})
    return items


def _format_highlights(current: Dict[str, int], previous: Dict[str, int]) -> List[Dict[str, Any]]:
    if sum(current.values()) == 0:
        return []
    items = [{"type": "TextBlock", "text": f"{Emojis.Sparkles.value} Highlights", "weight": "Bolder"}]
    for etype, label in [("app", "Application Errors"), ("node", "Node Errors"), ("pod", "Pod Errors")]:
        count = current.get(etype, 0)
        if count > 0:
            trend = calculate_trend(count, previous.get(etype, 0))
            items.append({"type": "TextBlock", "text": f"{label}: {count} {trend}", "wrap": True})
    return items


def get_teams_daily_highlight_template(params: DailyRecapParams) -> Dict[str, Any]:
    body: List[Dict[str, Any]] = [
        {
            "type": "TextBlock",
            "text": f"{params.title} - {params.organization_name}",
            "size": "Large",
            "weight": "Bolder",
            "wrap": True,
        }
    ]

    # Total Savings
    total_savings = 0
    if params.highlight1 and params.highlight1.data.recommendation_groupings.rows:
        total_savings = calculate_total_savings(params.highlight1.data.recommendation_groupings.rows)
    if total_savings > 0:
        body.append({"type": "TextBlock", "text": f"Potential Savings: ${total_savings:.2f}/month", "wrap": True})

    # Opportunity Lost
    total_opportunity_lost = params.total_opportunity_lost or 0
    if total_opportunity_lost > 0:
        body.append(
            {"type": "TextBlock", "text": f"Opportunity Lost (30 days): ${total_opportunity_lost:.2f}", "wrap": True}
        )

    # Prep data
    if not (params.highlight1 and params.insights):
        return _build_card(body)

    current_data = params.highlight1.data
    previous_data = params.highlight2.data if params.highlight2 else None
    account_name_map = {a.id: a.account_name for a in params.insights.data.cloud_accounts}

    current_app_events = group_events_by_account(current_data.app_events.rows)
    current_node_events = group_events_by_account(current_data.node_events.rows)
    current_pod_events = group_events_by_account(current_data.pod_events.rows)

    previous_app_events = group_events_by_account(previous_data.app_events.rows) if previous_data else {}
    previous_node_events = group_events_by_account(previous_data.node_events.rows) if previous_data else {}
    previous_pod_events = group_events_by_account(previous_data.pod_events.rows) if previous_data else {}

    recs_by_account = group_recommendations_by_account(current_data.recommendation_groupings.rows)
    insights_by_account = group_insights_by_account(params.insights.data.insight or [])

    unique_accounts = set(insights_by_account.keys()) | set(recs_by_account.keys())

    for account_id in unique_accounts:
        account_name = account_name_map.get(account_id, "")
        if not account_name:
            continue
        body.append(
            {
                "type": "TextBlock",
                "text": f"☁️ Cluster: {account_name}",
                "weight": "Bolder",
            }
        )
        body.extend(_format_insights(insights_by_account.get(account_id, [])))
        body.extend(_format_recommendations(recs_by_account.get(account_id, [])))
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
        body.extend(_format_highlights(current_events, prev_events))
        body.append({"type": "TextBlock", "text": "-----", "separator": True})

    # Footer link
    view_all_url = f"{public_ip()}/home"
    body.append({"type": "TextBlock", "text": f"[📊 View full report]({view_all_url})", "wrap": True})

    return _build_card(body)


def _build_card(body: List[Dict[str, Any]]) -> Dict[str, Any]:
    return {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.2",
        "msteams": {"width": "Full"},
        "body": body,
    }
