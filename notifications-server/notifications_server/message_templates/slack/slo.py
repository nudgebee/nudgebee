from datetime import datetime
from typing import List, Dict, Any, Optional, Union
from pydantic import BaseModel, field_validator

from notifications_server.configs.settings import public_ip
import logging

LOG = logging.getLogger(__name__)


class SLOAlertParams(BaseModel):
    account_id: str
    account_name: str
    namespace: str
    workload: str
    status: str
    slo_name: str
    slo_type: str
    slo_target: Union[str, float, int]
    current_value: Union[str, float, int]
    firing_since: Union[str, float, int]
    bad_event_count: int
    good_event_count: int
    threshold: Union[str, float, int]
    burn_rate: Optional[Union[str, float, int]] = None
    error_budget_remaining: Optional[Union[str, float, int]] = None
    end_time: Optional[Union[str, float, int]] = None

    @field_validator("firing_since", mode="before")
    @classmethod
    def parse_firing_since(cls, value):
        if isinstance(value, (int, float)):
            return value
        # Convert the firing_since string to a datetime object
        dt = datetime.strptime(value, "%Y-%m-%d %H:%M:%S")
        return dt.timestamp()


def get_slo_alert_message_params(**params) -> SLOAlertParams:
    params = SLOAlertParams(**params)
    return params


def get_slo_alert_message_template(params: SLOAlertParams):
    blocks: List[Dict[str, Any]] = []

    title_blocks = [
        {
            "type": "section",
            "text": add_markdown_block(f"*SLO Alert: {params.slo_name}*"),
        },
        {
            "type": "section",
            "fields": [
                add_markdown_block(
                    f"*Account:* <{public_ip()}/kubernetes/details/{params.account_id}|{params.account_name}>"
                ),
                add_markdown_block(f"*Namespace:* {params.namespace}"),
                add_markdown_block(f"*Workload:* {params.workload}"),
            ],
        },
        {"type": "divider"},
        {
            "type": "section",
            "fields": [
                add_markdown_block(f"*Status:* {params.status}"),
                add_markdown_block(f"*Target:* {params.slo_target}"),
                add_markdown_block(f"*Current Value:* {params.current_value}"),
                add_markdown_block(
                    f"*Firing Since: *<!date^{int(float(params.firing_since))}^{{date_short_pretty}} {{time}}|April"
                    " 14th, 2024 12:00 PM>"
                ),
                add_markdown_block(f"*Burn rate:* {params.burn_rate}"),
                add_markdown_block(f"*Budget Remaining:* {params.error_budget_remaining}"),
                add_markdown_block(f"*Threshold:* {params.threshold}"),
                add_markdown_block(f"*Good Events:* {params.good_event_count}"),
                add_markdown_block(f"*Bad Events:* {params.bad_event_count}"),
            ],
        },
    ]

    # Slack has a limit of 10 items per fields array, so split into chunks
    fields = title_blocks[3]["fields"]
    title_blocks[3]["fields"] = fields[:10]
    for i in range(10, len(fields), 10):
        chunk = fields[i : i + 10]
        title_blocks.append({"type": "section", "fields": chunk})

    title_blocks.extend(
        [
            {"type": "divider"},
            {
                "type": "section",
                "text": add_markdown_block(
                    "The current value of your SLO has dropped below the target of"
                    f" {params.slo_target}. The error budget is being consumed at a rate of `{params.burn_rate}`,"
                    f" leaving only `{params.error_budget_remaining}` of the budget remaining.\nImmediate action is"
                    " required to address this issue and ensure the reliability of your service."
                ),
            },
        ]
    )

    blocks.extend([*title_blocks, {"type": "divider"}])

    return {"text": "SLO Alert", "blocks": blocks, "unfurl_links": False}


def add_markdown_block(text):
    return {
        "type": "mrkdwn",
        "text": text,
    }
