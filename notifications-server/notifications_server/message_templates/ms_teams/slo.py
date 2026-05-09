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


def get_teams_slo_alert_template(params: SLOAlertParams) -> Dict[str, Any]:
    body: list[Any] = [
        {
            "type": "TextBlock",
            "text": f"{Emojis.Event.value} SLO Alert: *{params.slo_name}*",
            "size": "Large",
            "weight": "Bolder",
            "wrap": True,
        },
        {
            "type": "FactSet",
            "facts": [
                {
                    "title": "Account",
                    "value": f"[{params.account_name}]({public_ip()}/kubernetes/details/{params.account_id})",
                },
                {"title": "Namespace", "value": params.namespace},
                {"title": "Workload", "value": params.workload},
                {"title": "Status", "value": params.status},
                {"title": "Target", "value": params.slo_target},
                {"title": "Current Value", "value": params.current_value},
                {"title": "Firing Since", "value": params.firing_since},
                {"title": "Threshold", "value": params.threshold},
                {"title": "Good Events", "value": str(params.good_event_count)},
                {"title": "Bad Events", "value": str(params.bad_event_count)},
                {"title": "Burn Rate", "value": params.burn_rate or "N/A"},
                {"title": "Error Budget Remaining", "value": params.error_budget_remaining or "N/A"},
            ],
        },
        {
            "type": "TextBlock",
            "text": (
                f"{Emojis.Alert.value} The current value of your SLO has dropped below the target of"
                f" **{params.slo_target}**. The error budget is being consumed at a rate of **{params.burn_rate}**,"
                f" leaving only **{params.error_budget_remaining}** of the budget remaining. Immediate action is"
                " required to address this issue and ensure the reliability of your service."
            ),
            "wrap": True,
        },
    ]

    card_payload: Dict[str, Any] = {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.0",
        "msteams": {"width": "Full"},
        "body": body,
    }

    return card_payload
