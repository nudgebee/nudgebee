from typing import Dict, Any, List, Union
from datetime import datetime
import logging

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.base import Emojis
from notifications_server.message_templates.slack.cloud_cost_summary import CloudCostSummary, CURRENCY_SYMBOLS

LOG = logging.getLogger(__name__)


def format_currency(amount: Union[float, int], currency: str = "USD") -> str:
    symbol = CURRENCY_SYMBOLS.get(currency, currency)
    return f"{symbol}{amount:,.2f}"


def format_date(date_str: str) -> str:
    return datetime.strptime(date_str, "%Y-%m-%d").strftime("%b %d, %Y")


def get_cost_summary_facts(
    total_daily_cost: float, total_monthly_cost: float, account_id: str, period, currency: str = "USD"
) -> List[Dict[str, str]]:
    return [
        {"title": "Account ID", "value": account_id},
        {"title": "Period", "value": f"{format_date(period.start)} - {format_date(period.end)}"},
        {"title": "Daily Cost", "value": format_currency(total_daily_cost, currency)},
        {"title": "Monthly Cost", "value": format_currency(total_monthly_cost, currency)},
    ]


def get_top_items_container(title: str, items: List[Any], attr: str, currency: str = "USD") -> Dict[str, Any]:
    if not items:
        return {"type": "Container", "items": []}

    text_blocks = [{"type": "TextBlock", "text": f"**{title}**", "weight": "Bolder", "wrap": True}]

    for i, item in enumerate(items, 1):
        name = getattr(item, attr, "Unknown")
        cost = format_currency(getattr(item, "cost", 0), currency)
        text_blocks.append({"type": "TextBlock", "text": f"- **{name}:** {cost}", "wrap": True})

    return {"type": "Container", "items": text_blocks}


def get_teams_cloud_cost_message_template(params: CloudCostSummary) -> Dict[str, Any]:
    account_id = params.account_id
    summary = params.summary
    currency = params.cost_currency

    body: List[Dict[str, Any]] = [
        {
            "type": "TextBlock",
            "text": f"{Emojis.Dollar.value} {params.title}",
            "size": "Large",
            "weight": "Bolder",
            "wrap": True,
        },
        {
            "type": "FactSet",
            "facts": get_cost_summary_facts(
                params.total_daily_cost, params.total_monthly_cost, account_id, params.period, currency
            ),
        },
    ]

    body.extend(
        [
            get_top_items_container("Top 5 Daily Cost Items", summary.top_5_daily_items, "product_code", currency),
            get_top_items_container("Top 5 Monthly Services", summary.top_5_monthly_services, "service", currency),
        ]
    )

    cost_details_url = f"{public_ip()}/cloud-account/details/{account_id}?tab=0&utm=teams"
    actions = [
        {
            "type": "Action.OpenUrl",
            "title": f"{Emojis.Investigate.value} View Cost Details",
            "url": cost_details_url,
        }
    ]

    return {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.2",
        "msteams": {"width": "Full"},
        "body": body,
        "actions": actions,
    }
