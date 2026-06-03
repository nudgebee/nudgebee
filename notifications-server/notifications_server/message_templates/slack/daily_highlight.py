from typing import List, Dict, Any, Optional
from pydantic import BaseModel


class EventCounts(BaseModel):
    event_count: int
    account_id: str


class EventRows(BaseModel):
    rows: List[EventCounts]


class Recommendation(BaseModel):
    category: str
    rule_name: str
    count: int
    account_id: str
    sum_estimated_savings: float


class RecommendationRow(BaseModel):
    rows: List[Recommendation]


class Insight(BaseModel):
    account_id: str
    id: Optional[str] = None
    resource_id: Optional[str] = None
    source: Optional[str] = None
    status: Optional[str] = None
    title: str
    type: str
    unique_id: str
    applications: Optional[List[Dict[str, str]]] = None


class HighlightData(BaseModel):
    node_events: EventRows
    pod_events: EventRows
    app_events: EventRows
    recommendation_groupings: RecommendationRow


class Highlight(BaseModel):
    data: HighlightData


class Account(BaseModel):
    id: str
    account_name: str


class InsightsData(BaseModel):
    cloud_accounts: List[Account]
    insight: List[Insight]
    recommendation_security_groupings_v2: Optional[Dict[str, Any]] = None


class Insights(BaseModel):
    data: InsightsData
    errors: Optional[str]


class DailyRecapParams(BaseModel):
    highlight1: Optional[Highlight]
    highlight2: Optional[Highlight]
    insights: Optional[Insights]
    organization_id: str
    organization_name: str
    title: str
    total_opportunity_lost: Optional[float] = 0


def get_daily_recap_message_params(**params) -> DailyRecapParams:
    return DailyRecapParams(**params)


def create_section_block(section_text: str) -> Dict[str, Any]:
    return {"type": "section", "text": {"type": "mrkdwn", "text": section_text.strip()}}


def create_divider_block() -> Dict[str, Any]:
    return {"type": "divider"}


def format_rule_name(rule_name: str) -> str:
    """Format rule name for better readability"""
    return rule_name.replace("_", " ").title()


def calculate_trend(current: int, previous: int) -> str:
    """Calculate trend indicator for event counts"""
    if previous == 0:
        return "(-)"

    diff = previous - current
    if diff > 0:
        percentage = (diff / previous) * 100
        return f"(Down by {diff}, {percentage:.1f}%)"
    elif diff < 0:
        percentage = (-diff / previous) * 100
        return f"(Up by {-diff}, {percentage:.1f}%)"
    else:
        return "(-)"


def group_events_by_account(event_rows: List[EventCounts]) -> Dict[str, int]:
    """Group event counts by account_id"""
    return {event.account_id: event.event_count for event in event_rows}


def group_recommendations_by_account(recommendations: List[Recommendation]) -> Dict[str, List[Recommendation]]:
    """Group recommendations by account_id"""
    grouped = {}
    for rec in recommendations:
        if rec.account_id not in grouped:
            grouped[rec.account_id] = []
        grouped[rec.account_id].append(rec)
    return grouped


def group_insights_by_account(insights: List[Insight]) -> Dict[str, List[Insight]]:
    """Group insights by account_id"""
    grouped = {}
    for insight in insights:
        if insight.account_id not in grouped:
            grouped[insight.account_id] = []
        grouped[insight.account_id].append(insight)
    return grouped


def categorize_insights(insights: List[Insight]) -> Dict[str, List[Insight]]:
    """Categorize insights by type"""
    categorized = {"Troubleshooting": [], "Optimization": [], "Ops": []}
    for insight in insights:
        if insight.type in categorized:
            categorized[insight.type].append(insight)
    return categorized


def add_insights_blocks(blocks: List[Dict[str, Any]], insights: List[Insight]) -> None:
    """Add insights blocks to the list"""
    categorized = categorize_insights(insights)

    if any(categorized.values()):
        blocks.append(create_section_block("*Insights:*"))

        for insight in categorized["Troubleshooting"]:
            app_count = len(insight.applications) if insight.applications else 0
            text = f"• {insight.title} detected for ({app_count}) workloads" if app_count > 0 else f"• {insight.title}"
            blocks.append(create_section_block(text))

        for insight in categorized["Optimization"]:
            blocks.append(create_section_block(f"• {insight.title}"))

        for insight in categorized["Ops"]:
            blocks.append(create_section_block(f"• {insight.title}"))


def add_recommendations_blocks(blocks: List[Dict[str, Any]], recommendations: List[Recommendation]) -> None:
    """Add recommendations blocks to the list"""
    if not recommendations:
        return

    blocks.append(create_section_block("*Recommendations:*"))
    sorted_recs = sorted(recommendations, key=lambda x: x.sum_estimated_savings, reverse=True)[:5]

    for rec in sorted_recs:
        if rec.sum_estimated_savings > 0:
            text = (
                f"• *{rec.count}* {format_rule_name(rec.rule_name)} recommendations – "
                f"potential savings: *${rec.sum_estimated_savings:.2f}*/month"
            )
        else:
            text = f"• *{rec.count}* {format_rule_name(rec.rule_name)} recommendations available"
        blocks.append(create_section_block(text))


def add_highlights_blocks(
    blocks: List[Dict[str, Any]], current_events: Dict[str, int], previous_events: Dict[str, int]
) -> None:
    """Add highlights blocks to the list"""
    total_current_events = sum(current_events.values())
    if total_current_events == 0:
        return

    blocks.append(create_section_block("*Highlights:*"))
    event_texts = []

    for event_type, label in [("app", "Application Errors"), ("node", "Node Errors"), ("pod", "Pod Errors")]:
        count = current_events.get(event_type, 0)
        if count > 0:
            trend = calculate_trend(count, previous_events.get(event_type, 0))
            event_texts.append(f"{label}: {count} {trend}")

    if event_texts:
        blocks.append(create_section_block("• " + ", ".join(event_texts)))


def create_blocks_for_account(
    account_name: str,
    current_events: Dict[str, int],
    previous_events: Dict[str, int],
    recommendations: List[Recommendation],
    insights: List[Insight],
) -> List[Dict[str, Any]]:
    blocks = [create_section_block(f"*Cluster: {account_name}*")]

    add_insights_blocks(blocks, insights)
    add_recommendations_blocks(blocks, recommendations)
    add_highlights_blocks(blocks, current_events, previous_events)

    blocks.append(create_divider_block())
    return blocks


def calculate_total_savings(recommendations: List[Recommendation]) -> float:
    """Calculate total potential savings from recommendations"""
    return sum(rec.sum_estimated_savings for rec in recommendations if rec.sum_estimated_savings > 0)


def create_header_blocks(
    title: str, organization_name: str, total_savings: float, total_opportunity_lost: float = 0
) -> List[Dict[str, Any]]:
    """Create header blocks for the message"""
    blocks = [create_section_block(f"*{title} - {organization_name}*")]

    if total_savings > 0:
        blocks.append(create_section_block(f"*Potential Savings: ${total_savings:.2f}/month*"))

    if total_opportunity_lost > 0:
        blocks.append(create_section_block(f"*Opportunity Lost (30 days): ${total_opportunity_lost:.2f}*"))

    blocks.append(
        create_section_block(
            "Here's your daily recap for all your clusters. See how clusters are performing and opportunities to"
            " optimize!"
        )
    )
    blocks.append(create_divider_block())

    return blocks


def process_account_data(params: DailyRecapParams) -> List[Dict[str, Any]]:
    """Process account data and create blocks"""
    if not (params.highlight1 and params.insights):
        return []

    current_data = params.highlight1.data
    previous_data = params.highlight2.data if params.highlight2 else None
    all_insights = params.insights.data.insight or []
    account_name_map = {account.id: account.account_name for account in params.insights.data.cloud_accounts}

    # Group data by account for O(1) lookups
    current_app_events = group_events_by_account(current_data.app_events.rows)
    current_node_events = group_events_by_account(current_data.node_events.rows)
    current_pod_events = group_events_by_account(current_data.pod_events.rows)

    previous_app_events = group_events_by_account(previous_data.app_events.rows) if previous_data else {}
    previous_node_events = group_events_by_account(previous_data.node_events.rows) if previous_data else {}
    previous_pod_events = group_events_by_account(previous_data.pod_events.rows) if previous_data else {}

    recommendations_by_account = group_recommendations_by_account(current_data.recommendation_groupings.rows)
    insights_by_account = group_insights_by_account(all_insights)

    # Get unique account IDs
    unique_account_ids = set(insights_by_account.keys()) | set(recommendations_by_account.keys())

    blocks = []
    for account_id in unique_account_ids:
        account_name = account_name_map.get(account_id, "")
        if not account_name:
            continue

        current_events = {
            "app": current_app_events.get(account_id, 0),
            "node": current_node_events.get(account_id, 0),
            "pod": current_pod_events.get(account_id, 0),
        }

        previous_events = {
            "app": previous_app_events.get(account_id, 0),
            "node": previous_node_events.get(account_id, 0),
            "pod": previous_pod_events.get(account_id, 0),
        }

        account_recommendations = recommendations_by_account.get(account_id, [])
        account_insights = insights_by_account.get(account_id, [])

        # Check if this account has any data to show
        if sum(current_events.values()) > 0 or account_recommendations or account_insights:
            blocks.extend(
                create_blocks_for_account(
                    account_name, current_events, previous_events, account_recommendations, account_insights
                )
            )

    return blocks


def create_account_attachment(
    account_name: str,
    current_events: Dict[str, int],
    previous_events: Dict[str, int],
    recommendations: List[Recommendation],
    insights: List[Insight],
) -> Dict[str, Any]:
    """Create a collapsible attachment for account data."""
    blocks = []
    add_insights_blocks(blocks, insights)
    add_recommendations_blocks(blocks, recommendations)
    add_highlights_blocks(blocks, current_events, previous_events)

    # Calculate summary for fallback text
    total_events = sum(current_events.values())
    rec_count = len(recommendations)
    insight_count = len(insights)

    fallback_parts = []
    if insight_count > 0:
        fallback_parts.append(f"{insight_count} insights")
    if rec_count > 0:
        fallback_parts.append(f"{rec_count} recommendations")
    if total_events > 0:
        fallback_parts.append(f"{total_events} events")

    fallback = f"{account_name}: " + (", ".join(fallback_parts) if fallback_parts else "No activity")

    return {
        "color": "#36a64f",
        "fallback": fallback,
        "blocks": [{"type": "section", "text": {"type": "mrkdwn", "text": f"*Cluster: {account_name}*"}}] + blocks,
    }


def process_account_data_as_attachments(params: DailyRecapParams) -> List[Dict[str, Any]]:
    """Process account data and create collapsible attachments for each account."""
    if not (params.highlight1 and params.insights):
        return []

    current_data = params.highlight1.data
    previous_data = params.highlight2.data if params.highlight2 else None
    all_insights = params.insights.data.insight or []
    account_name_map = {account.id: account.account_name for account in params.insights.data.cloud_accounts}

    # Group data by account for O(1) lookups
    current_app_events = group_events_by_account(current_data.app_events.rows)
    current_node_events = group_events_by_account(current_data.node_events.rows)
    current_pod_events = group_events_by_account(current_data.pod_events.rows)

    previous_app_events = group_events_by_account(previous_data.app_events.rows) if previous_data else {}
    previous_node_events = group_events_by_account(previous_data.node_events.rows) if previous_data else {}
    previous_pod_events = group_events_by_account(previous_data.pod_events.rows) if previous_data else {}

    recommendations_by_account = group_recommendations_by_account(current_data.recommendation_groupings.rows)
    insights_by_account = group_insights_by_account(all_insights)

    # Get unique account IDs
    unique_account_ids = set(insights_by_account.keys()) | set(recommendations_by_account.keys())

    attachments = []
    for account_id in unique_account_ids:
        account_name = account_name_map.get(account_id, "")
        if not account_name:
            continue

        current_events = {
            "app": current_app_events.get(account_id, 0),
            "node": current_node_events.get(account_id, 0),
            "pod": current_pod_events.get(account_id, 0),
        }

        previous_events = {
            "app": previous_app_events.get(account_id, 0),
            "node": previous_node_events.get(account_id, 0),
            "pod": previous_pod_events.get(account_id, 0),
        }

        account_recommendations = recommendations_by_account.get(account_id, [])
        account_insights = insights_by_account.get(account_id, [])

        # Check if this account has any data to show
        if sum(current_events.values()) > 0 or account_recommendations or account_insights:
            attachments.append(
                create_account_attachment(
                    account_name, current_events, previous_events, account_recommendations, account_insights
                )
            )

    return attachments


def get_daily_recap_message_template(params: DailyRecapParams):
    total_savings = 0
    if params.highlight1 and params.highlight1.data.recommendation_groupings.rows:
        total_savings = calculate_total_savings(params.highlight1.data.recommendation_groupings.rows)

    total_opportunity_lost = params.total_opportunity_lost or 0
    blocks = create_header_blocks(params.title, params.organization_name, total_savings, total_opportunity_lost)
    attachments = process_account_data_as_attachments(params)

    return {
        "text": "Daily Recap",
        "blocks": blocks[:50],
        "attachments": attachments[:20],  # Slack limit on attachments
        "unfurl_links": False,
    }
