"""The api-server sends the window and threshold of the multi-window burn-rate
rule that actually fired. Pydantic ignores extra keys, so a template that does
not read them drops them silently — these tests pin that they are rendered.
"""

import json

from notifications_server.message_templates.base import format_burn_rate, format_burn_rate_window
from notifications_server.message_templates.google_chat.slo import get_gchat_slo_alert_template
from notifications_server.message_templates.ms_teams.slo import get_teams_slo_alert_template
from notifications_server.message_templates.slack.slo import SLOAlertParams, get_slo_alert_message_template

BASE_PARAMS = {
    "account_id": "acc-1",
    "account_name": "prod-cluster",
    "namespace": "payments",
    "workload": "checkout",
    "status": "FIRING",
    "slo_name": "availability",
    "slo_type": "availability",
    "slo_target": 0.99,
    "current_value": "0.94",
    "firing_since": 1786254854,
    "bad_event_count": 120,
    "good_event_count": 1880,
    "threshold": 500,
    "burn_rate": 14,
    "error_budget_remaining": 12,
}


def _params(**overrides) -> SLOAlertParams:
    return SLOAlertParams(**{**BASE_PARAMS, **overrides})


def _text(payload) -> str:
    # ensure_ascii would escape the × the templates render.
    return json.dumps(payload, ensure_ascii=False)


def test_format_burn_rate_window():
    assert format_burn_rate_window(3600) == "1 hour"
    assert format_burn_rate_window(21600) == "6 hours"
    assert format_burn_rate_window(300) == "5 minutes"
    assert format_burn_rate_window(60) == "1 minute"
    # No window is a legitimate state (older api-server), not an error.
    assert format_burn_rate_window(None) is None
    assert format_burn_rate_window(0) is None
    assert format_burn_rate_window("not-a-number") is None


def test_format_burn_rate():
    assert format_burn_rate(14.4, 3600) == "14.4× over 1 hour"
    assert format_burn_rate(14.4, None) == "14.4×"
    assert format_burn_rate(None, 3600) is None
    assert format_burn_rate("", 3600) is None


def test_slack_card_names_the_window_and_threshold():
    rendered = _text(get_slo_alert_message_template(_params(burn_rate_window=21600, burn_rate_threshold=6)))
    assert "14× over 6 hours" in rendered
    assert "Burn-rate threshold" in rendered and "6×" in rendered
    assert "over the last 6 hours" in rendered


def test_teams_card_names_the_window_and_threshold():
    rendered = _text(get_teams_slo_alert_template(_params(burn_rate_window=3600, burn_rate_threshold=14.4)))
    assert "14× over 1 hour" in rendered
    assert "Burn-rate Threshold" in rendered and "14.4×" in rendered


def test_gchat_message_names_the_window_and_threshold():
    rendered = get_gchat_slo_alert_template(_params(burn_rate_window=3600, burn_rate_threshold=14.4))["text"]
    assert "*Burn Rate*: 14× over 1 hour" in rendered
    assert "*Burn-rate Threshold*: 14.4×" in rendered


def test_renders_without_the_new_fields():
    """An api-server that predates multi-window evaluation sends neither key."""
    slack = _text(get_slo_alert_message_template(_params()))
    teams = _text(get_teams_slo_alert_template(_params()))
    gchat = get_gchat_slo_alert_template(_params())["text"]
    for rendered in (slack, teams, gchat):
        assert "14×" in rendered
        assert "× over" not in rendered
        assert "over the last" not in rendered
    assert "Burn-rate threshold" not in slack
    assert "Burn-rate Threshold" not in teams and "Burn-rate Threshold" not in gchat
