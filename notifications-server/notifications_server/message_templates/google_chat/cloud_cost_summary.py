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


def format_top_items(items: List[Any], title: str, attr: str, currency: str = "USD") -> List[str]:
    if not items:
        return []

    lines = [f"*{title}:*"]
    for i, item in enumerate(items, 1):
        name = getattr(item, attr, "Unknown")
        cost = format_currency(getattr(item, "cost", 0), currency)
        lines.append(f"- *{name}*: {cost}")

    lines.append("")  # Spacer
    return lines


def format_date(date_str: str) -> str:
    return datetime.strptime(date_str, "%Y-%m-%d").strftime("%b %d, %Y")


def get_cloud_cost_gchat_message_template(params: CloudCostSummary) -> Dict[str, Any]:
    account_id = params.account_id
    title = params.title
    summary = params.summary
    currency = params.cost_currency

    # Period formatting
    start_date, end_date = map(format_date, (params.period.start, params.period.end))

    lines = [
        f"{Emojis.Dollar.value} *{title}*",
        "",
        f"*Account ID:* {account_id}",
        f"*Period:* {start_date} - {end_date}",
        f"*Daily Cost:* {format_currency(params.total_daily_cost, currency)}",
        f"*Monthly Cost:* {format_currency(params.total_monthly_cost, currency)}",
        "",
        "-" * 25,
        "",
    ]

    lines.extend(format_top_items(summary.top_5_daily_items, "Top 5 Daily Cost Items", "product_code", currency))
    lines.extend(format_top_items(summary.top_5_monthly_services, "Top 5 Monthly Services", "service", currency))

    cost_details_url = f"{public_ip()}/cloud-account/details/{account_id}?tab=0&utm=gchat"
    lines.append(f"{Emojis.Investigate.value} View detailed cost analysis: {cost_details_url}")

    return {"text": "\n".join(lines).strip()}
