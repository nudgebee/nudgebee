"""Discord renderer for the daily cloud cost summary.

Mirrors message_templates/google_chat/cloud_cost_summary.py, reusing the Slack
template's currency/date formatting and cost-details deep link. Returns the
{"content", "embeds"} payload consumed by DiscordClient.chat_post.
"""

from typing import Any, Dict, List

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.discord.embed_utils import clamp_embeds
from notifications_server.message_templates.slack.cloud_cost_summary import (
    CloudCostSummary,
    format_currency,
    format_date,
)

CLOUD_COST_COLOR = 15844367  # gold (#f1c40f)


def _top_items_lines(title: str, items: List[Any], attr: str, currency: str) -> List[str]:
    if not items:
        return []
    lines = [f"**{title}:**"]
    for item in items:
        name = getattr(item, attr, "Unknown")
        cost = format_currency(getattr(item, "cost", 0), currency)
        lines.append(f"- **{name}:** {cost}")
    lines.append("")
    return lines


def get_discord_cloud_cost_message_template(params: CloudCostSummary) -> Dict[str, Any]:
    account_name = params.account_name or "Cloud Account"
    currency = params.cost_currency
    cost_details_url = f"{public_ip()}/cloud-account/details/{params.account_id}?tab=0&utm=discord"

    lines = [
        f"**Account Name:** {account_name}",
        f"**Period:** {format_date(params.period.start)} - {format_date(params.period.end)}",
        f"**Daily Cost:** {format_currency(params.total_daily_cost, currency)}",
        f"**Monthly Cost:** {format_currency(params.total_monthly_cost, currency)}",
        "",
    ]
    lines.extend(_top_items_lines("Top 5 Daily Cost Items", params.summary.top_5_daily_items, "product_code", currency))
    lines.extend(_top_items_lines("Top 5 Monthly Services", params.summary.top_5_monthly_services, "service", currency))
    lines.append(f"[View Cost Details]({cost_details_url})")

    embed = {
        "title": params.title,
        "description": "\n".join(lines),
        "color": CLOUD_COST_COLOR,
    }
    return {"content": f"💲 {params.title}", "embeds": clamp_embeds([embed])}
