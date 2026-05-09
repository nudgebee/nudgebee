from datetime import datetime
from typing import List, Dict, Any, Optional, Union
from pydantic import BaseModel, field_validator

from notifications_server.configs.settings import public_ip
from notifications_server.message_templates.base import Emojis
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


def get_gchat_slo_alert_template(params: SLOAlertParams) -> Dict[str, str]:
    lines = []
    # Header
    lines.append(f"{Emojis.Event.value} *SLO Alert*: {params.slo_name} for {params.account_name}")

    # Core SLO details
    lines.append(f"*Status*: {params.status}")
    lines.append(f"*Target*: {params.slo_target}")
    lines.append(f"*Current Value*: {params.current_value}")
    lines.append(f"*Firing Since*: {params.firing_since}")
    lines.append(f"*Threshold*: {params.threshold}")
    lines.append(f"*Good Events*: {params.good_event_count}  *Bad Events*: {params.bad_event_count}")

    # Optional fields
    if params.burn_rate:
        lines.append(f"*Burn Rate*: {params.burn_rate}")
    if params.error_budget_remaining:
        lines.append(f"*Error Budget Remaining*: {params.error_budget_remaining}")
    if params.end_time:
        lines.append(f"*End Time*: {params.end_time}")

    # Spacer
    lines.append("")

    # Alert narrative
    narrative = (
        f"{Emojis.Alert.value} The current value of your SLO has dropped below the target of {params.slo_target}. The"
        f" error budget is being consumed at a rate of {params.burn_rate or 'N/A'}, leaving only"
        f" {params.error_budget_remaining or 'N/A'}. Immediate action is required to address this issue and ensure the"
        " reliability of your service."
    )
    lines.append(narrative)

    text = "\n".join(lines)
    return {"text": text}
