from typing import Dict, Any, List, Union, Optional
from datetime import datetime
import logging
from pydantic import BaseModel

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_NEUTRAL,
    header_block,
    legacy_attachment,
    link_button,
)

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


def _top_items_line(label: str, items: List[Any], attr: str, currency: str) -> str:
    if not items:
        return ""
    parts = [f"{getattr(it, attr, 'Unknown')} {format_currency(getattr(it, 'cost', 0), currency)}" for it in items[:5]]
    return f"*{label}:* " + " · ".join(parts)


def get_cloud_cost_message_template(params: CloudCostSummary) -> Dict[str, Any]:
    account_name = getattr(params, "account_name", None) or "Cloud Account"
    currency = params.cost_currency

    fields = [
        {"title": "Account", "value": account_name, "short": True},
        {
            "title": "Period",
            "value": f"{format_date(params.period.start)} – {format_date(params.period.end)}",
            "short": True,
        },
        {"title": "Daily cost", "value": format_currency(params.total_daily_cost, currency), "short": True},
        {"title": "Monthly cost", "value": format_currency(params.total_monthly_cost, currency), "short": True},
    ]

    lines = [
        line
        for line in (
            _top_items_line("Top daily", params.summary.top_5_daily_items, "product_code", currency),
            _top_items_line("Top monthly", params.summary.top_5_monthly_services, "service", currency),
        )
        if line
    ]

    url = f"{public_ip()}/cloud-account/details/{params.account_id}?tab=0&utm=slack"
    attachment = legacy_attachment(
        STRIPE_NEUTRAL,
        params.title,
        text="\n".join(lines),
        fields=fields,
        actions=[link_button("View Cost Details", url)],
    )
    return {"blocks": [header_block(params.title)], "attachments": [attachment], "unfurl_links": False}
