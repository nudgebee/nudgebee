from typing import Dict, Any, List, Union, Optional
from datetime import datetime
import logging
from pydantic import BaseModel

from notifications_server.configs.settings import public_ip

LOG = logging.getLogger(__name__)


class DailyItem(BaseModel):
    cost: float
    product_code: str
    resource_arn: str = ""


class MonthlyService(BaseModel):
    cost: float
    service: str


class CostSummary(BaseModel):
    top_5_daily_items: List[DailyItem] = []
    top_5_monthly_services: List[MonthlyService] = []


class CostPeriod(BaseModel):
    start: str
    end: str


CURRENCY_SYMBOLS = {
    "USD": "$",
    "INR": "\u20b9",
    "EUR": "\u20ac",
    "GBP": "\u00a3",
    "JPY": "\u00a5",
    "AUD": "A$",
    "CAD": "C$",
}


class CloudCostSummary(BaseModel):
    account_id: str
    account_name: Optional[str] = None
    period: CostPeriod
    title: str
    total_daily_cost: float
    total_monthly_cost: float
    summary: CostSummary
    cost_currency: str = "USD"


def get_cloud_cost_summary_message_params(**params) -> CloudCostSummary:
    return CloudCostSummary(**params)


def format_currency(amount: Union[float, int], currency: str = "USD") -> str:
    symbol = CURRENCY_SYMBOLS.get(currency, currency)
    return f"{symbol}{amount:,.2f}"


def format_date(date_str: str) -> str:
    return datetime.strptime(date_str, "%Y-%m-%d").strftime("%b %d, %Y")


def get_title_block(title: str) -> Dict[str, Any]:
    return {
        "type": "section",
        "text": {"type": "mrkdwn", "text": f"*{title}*"},
    }


def get_summary_block(
    total_daily_cost: float, total_monthly_cost: float, account_name: str, period: CostPeriod, currency: str = "USD"
) -> Dict[str, Any]:
    return {
        "type": "section",
        "fields": [
            {"type": "mrkdwn", "text": f"*Account Name:* {account_name}"},
            {"type": "mrkdwn", "text": f"*Period:* {format_date(period.start)} - {format_date(period.end)}"},
            {"type": "mrkdwn", "text": f"*Daily Cost:* {format_currency(total_daily_cost, currency)}"},
            {"type": "mrkdwn", "text": f"*Monthly Cost:* {format_currency(total_monthly_cost, currency)}"},
        ],
    }


def get_top_items_block(title: str, items: List[Any], attr: str, currency: str = "USD") -> List[Dict[str, Any]]:
    if not items:
        return []

    header = {
        "type": "section",
        "text": {"type": "mrkdwn", "text": f"*{title}:*"},
    }

    lines = []
    for i, item in enumerate(items, 1):
        name = getattr(item, attr, "Unknown")
        cost = format_currency(getattr(item, "cost", 0), currency)
        lines.append(f"- *{name}:* {cost}")

    body = {
        "type": "section",
        "text": {"type": "mrkdwn", "text": "\n".join(lines)},
    }

    return [header, body]


def get_action_buttons(account_id: str) -> Dict[str, Any]:
    base_url = public_ip()
    return {
        "type": "actions",
        "elements": [
            {
                "type": "button",
                "text": {"type": "plain_text", "text": "View Cost Details", "emoji": False},
                "url": f"{base_url}/cloud-account/details/{account_id}?tab=0&utm=slack",
                "action_id": "view_cost_details",
            }
        ],
    }


def get_cloud_cost_message_template(params: CloudCostSummary) -> Dict[str, Any]:
    account_name = getattr(params, "account_name", None) or "Cloud Account"
    currency = params.cost_currency
    blocks: List[Dict[str, Any]] = [
        get_title_block(params.title),
        {"type": "divider"},
        get_summary_block(params.total_daily_cost, params.total_monthly_cost, account_name, params.period, currency),
        {"type": "divider"},
    ]

    daily_blocks = get_top_items_block(
        "Top 5 Daily Cost Items", params.summary.top_5_daily_items, "product_code", currency
    )
    if daily_blocks:
        blocks.extend(daily_blocks)
        blocks.append({"type": "divider"})

    monthly_blocks = get_top_items_block(
        "Top 5 Monthly Services", params.summary.top_5_monthly_services, "service", currency
    )
    if monthly_blocks:
        blocks.extend(monthly_blocks)
        blocks.append({"type": "divider"})

    blocks.append(get_action_buttons(params.account_id))

    return {"text": "Cloud Cost Report", "blocks": blocks, "unfurl_links": False}
