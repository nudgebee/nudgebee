"""A recommendation that raises resource requests costs money to apply.

estimated_savings carries the signed cost impact, so the notification templates
must not render a cost increase through the savings wording — "Savings: $-38.13/mo"
reads as a negative saving rather than a positive cost. Covers every platform that
renders a per-recommendation savings figure.
"""

import pytest

from notifications_server.message_templates.discord.recommendation_resolution import (
    get_discord_recommendation_resolution_template,
)
from notifications_server.message_templates.google_chat.recommendation_resolution import (
    get_gchat_recommendation_resolution_template,
)
from notifications_server.message_templates.ms_teams.recommendation_resolution import (
    get_teams_recommendation_resolution_template,
)
from notifications_server.message_templates.slack.recommendation_nudge_digest import format_savings_clause
from notifications_server.message_templates.slack.recommendation_resolution import (
    RecommendationResolutionParams,
)


@pytest.mark.parametrize(
    "amount,expected",
    [
        (212.98, "Savings: $212.98/mo"),
        (0.0, "Savings: $0/mo"),
        (-38.13, "Costs $38.13/mo more"),
        (-1200.0, "Costs $1,200/mo more"),
    ],
)
def test_clause_wording_follows_the_direction(amount, expected):
    assert format_savings_clause(amount) == expected


def _params(savings):
    return RecommendationResolutionParams(
        recommendation_id="rec-1",
        resource_name="nudgebee-agent-prod-opentelemetry-collector",
        rule_name="pod_right_sizing",
        account_name="k8s-prod",
        severity="Critical",
        estimated_savings=savings,
        base_url="https://example.invalid",
    )


def _text(payload):
    import json

    return json.dumps(payload)


@pytest.mark.parametrize(
    "render",
    [
        get_discord_recommendation_resolution_template,
        get_gchat_recommendation_resolution_template,
        get_teams_recommendation_resolution_template,
    ],
)
def test_cost_increase_is_never_rendered_as_a_negative_saving(render):
    body = _text(render(_params(-38.13)))
    assert "$-38" not in body
    assert "-$38" not in body
    assert "38.13" in body


@pytest.mark.parametrize(
    "render",
    [
        get_discord_recommendation_resolution_template,
        get_gchat_recommendation_resolution_template,
        get_teams_recommendation_resolution_template,
    ],
)
def test_real_savings_still_render_as_savings(render):
    body = _text(render(_params(212.98)))
    assert "212.98" in body
    assert "Costs" not in body
