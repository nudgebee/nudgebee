from datetime import datetime, timezone
from typing import List, Dict, Any, Optional, Union
from pydantic import BaseModel, field_validator

from notifications_server.configs.settings import URLRoutes, settings
from notifications_server.message_templates.base import format_burn_rate, format_burn_rate_window
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_CRITICAL,
    legacy_attachment,
    link_button,
)
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
    # Window (seconds) and threshold of the multi-window burn-rate rule that
    # fired. Absent on notifications from an api-server that predates them.
    burn_rate_window: Optional[Union[str, float, int]] = None
    burn_rate_threshold: Optional[Union[str, float, int]] = None
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


def _firing_since_field(firing_since: Union[str, float, int]) -> Optional[Dict[str, Any]]:
    """Wide field with Slack's localized <!date> token; computed fallback text
    (not a hardcoded date) for clients that can't render it."""
    try:
        ts = int(float(firing_since))
        fallback = datetime.fromtimestamp(ts, tz=timezone.utc).strftime("%d %b %Y %I:%M %p UTC")
    except (TypeError, ValueError, OSError, OverflowError):
        return None
    return {
        "title": "Firing since",
        "value": f"<!date^{ts}^{{date_short_pretty}} {{time}}|{fallback}>",
        "short": False,
    }


def _short_field(title: str, value: Any) -> Optional[Dict[str, Any]]:
    if value in (None, "", "N/A"):
        return None
    return {"title": title, "value": str(value), "short": True}


def get_slo_alert_message_template(params: SLOAlertParams):
    """Single severity-striped SLO card (matches the finding-card standard):
    clickable title, all SLO stats as two-column fields, a concise burn
    summary, and a View SLO button — all inside one red legacy attachment."""
    slo_monitoring_url = settings.urls.cluster_details_url(
        params.account_id,
        utm_source=URLRoutes.UTMSource.SLACK,
        anchor=URLRoutes.Anchors.MONITORING_SLO,
    )

    workload = f"{params.namespace}/{params.workload}" if params.namespace else params.workload
    events = None
    if params.bad_event_count is not None or params.good_event_count is not None:
        events = f"{params.bad_event_count} bad / {params.good_event_count} good"

    # Name the window that actually breached — "14× over 1 hour" is actionable
    # in a way a bare multiplier is not.
    window = format_burn_rate_window(params.burn_rate_window)
    burn_rate = format_burn_rate(params.burn_rate, params.burn_rate_window)
    burn_rate_threshold = f"{params.burn_rate_threshold}×" if params.burn_rate_threshold else None

    fields = [
        f
        for f in [
            _short_field("Account", params.account_name),
            _short_field("Workload", workload),
            _short_field("Target", params.slo_target),
            _short_field("Current", params.current_value),
            _short_field("Burn rate", burn_rate),
            _short_field("Burn-rate threshold", burn_rate_threshold),
            _short_field("Budget remaining", params.error_budget_remaining),
            _short_field("Threshold", params.threshold),
            _short_field("Events (bad/good)", events),
            _firing_since_field(params.firing_since),
        ]
        if f is not None
    ]

    over_window = f" over the last {window}" if window else ""
    summary = (
        f"Error budget is being consumed at `{params.burn_rate}×`{over_window}, leaving"
        f" `{params.error_budget_remaining}` remaining. Immediate action is required to protect the service."
    )

    attachment = legacy_attachment(
        STRIPE_CRITICAL,
        f"SLO breach: {params.slo_name}",
        text=summary,
        fields=fields,
        actions=[link_button("View SLO", slo_monitoring_url, style="primary")],
    )
    attachment["title"] = f"SLO breach: {params.slo_name}"
    attachment["title_link"] = slo_monitoring_url

    return {"attachments": [attachment], "unfurl_links": False}
