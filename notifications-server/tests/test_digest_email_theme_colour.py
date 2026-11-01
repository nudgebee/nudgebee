"""The scheduled digest emails take their link and button colour from branding.

`email.primaryColor` in the branding row already coloured the magic-link
button. The three digests (events summary, daily recap, agent status) spelled
out a fixed blue instead, so a rebranded host still sent blue links.
"""

import copy
from pathlib import Path

import pytest

from notifications_server.configs.settings import settings
from notifications_server.emailer.generator import get_templates_path, render_template_html
from notifications_server.emailer.template_params import _tint

ACCOUNT_ID = "acct-1"
DIGESTS = ["events_summary", "daily_highlight_report", "agent_status_report"]

BRAND_COLOUR = "#7A00E6"
BRAND_TINT = "#E8D4FB"
DEFAULT_COLOUR = "#3470e9"
DEFAULT_TINT = "#DCE7FB"

# The blue the digests used to spell out, and its pale pill background.
OLD_COLOURS = ("#3B82F6", "#DBEAFE")

PARAMS = {
    # One critical event group: an "Investigate" pill and a "show all" link.
    "events_summary": {
        "title": "Events Summary",
        "base_url": "https://app.example.com",
        "total_events": 4,
        "total_high_events": 4,
        "accounts": {"data": {"accounts": [{"id": ACCOUNT_ID, "account_name": "prod"}]}},
        "event_counts": {
            "data": {
                "event_groupings": {
                    "rows": [
                        {
                            "account_id": ACCOUNT_ID,
                            "aggregation_key": "report_crash_loop",
                            "event_count": 4,
                            "count_priority_high": 4,
                            "count_priority_medium": 0,
                            "count_priority_low": 0,
                        }
                    ]
                }
            }
        },
        "events_summarised": {
            "data": {
                "event_groupings": {
                    "rows": [
                        {
                            "account_id": ACCOUNT_ID,
                            "aggregation_key": "report_crash_loop",
                            "subject_name": "checkout",
                            "subject_namespace": "shop",
                            "event_count": 4,
                            "max_created_at": "2026-10-02T08:00:00Z",
                        }
                    ]
                }
            }
        },
    },
    # One recommendation: a "View" link in the overview, a rule link and an "Apply" pill.
    "daily_highlight_report": {
        "title": "Daily highlight report",
        "base_url": "https://app.example.com",
        "cluster_count": 1,
        "attention_count": 1,
        "insights": {
            "data": {
                "cloud_accounts": [{"id": ACCOUNT_ID, "account_name": "prod"}],
                "recommendation_security_groupings_v2": {"rows": []},
            }
        },
        "highlight1": {
            "data": {
                "app_events": {"rows": []},
                "node_events": {"rows": []},
                "pod_events": {"rows": []},
                "recommendation_groupings": {
                    "rows": [
                        {
                            "account_id": ACCOUNT_ID,
                            "rule_name": "pod_right_sizing",
                            "category": "RightSizing",
                            "count": 3,
                            "sum_estimated_savings": 12.5,
                        }
                    ]
                },
            }
        },
        "highlight2": {"data": {"app_events": {"rows": []}, "node_events": {"rows": []}, "pod_events": {"rows": []}}},
    },
    # A running agent with one integration down: a "Reconnect integrations" pill.
    "agent_status_report": {
        "title": "Agent Status Report",
        "agent_total": 1,
        "agent_disconnected": 0,
        "agent_partial": 1,
        "accounts": [
            {
                "account_name": "prod",
                "agent_details": {
                    "data": {
                        "agent": [
                            {
                                "status": "RUNNING",
                                "last_connected_at": "2026-10-02T08:00:00Z",
                                "version": "1.0.0",
                                "k8s_provider": "eks",
                                "k8s_version": "1.31",
                                "connection_status": {
                                    "alertManagerConnection": True,
                                    "grafanaEnabled": False,
                                    "karpenterEnabled": True,
                                    "logsConnection": True,
                                    "opencostConnection": True,
                                },
                            }
                        ]
                    }
                },
            }
        ],
    },
}


def _render(template):
    # The generator formats dates in place, so each render gets its own copy.
    return render_template_html(copy.deepcopy(PARAMS[template]), template)


@pytest.mark.parametrize("template", DIGESTS)
def test_digest_uses_the_brand_colour(template, monkeypatch):
    monkeypatch.setattr(settings.urls, "branding_primary_color", BRAND_COLOUR)

    html = _render(template)

    assert f"color:{BRAND_COLOUR};" in html
    assert f"background-color:{BRAND_TINT};" in html
    for old in OLD_COLOURS:
        assert old not in html


@pytest.mark.parametrize("template", DIGESTS)
def test_digest_keeps_blue_for_the_default_brand(template, monkeypatch):
    monkeypatch.setattr(settings.urls, "branding_primary_color", DEFAULT_COLOUR)

    html = _render(template)

    assert f"color:{DEFAULT_COLOUR};" in html
    assert f"background-color:{DEFAULT_TINT};" in html


@pytest.mark.parametrize("template", DIGESTS)
def test_digest_template_has_no_fixed_link_colour(template):
    # Covers the branches the sample data above does not reach.
    source = (Path(get_templates_path()) / f"{template}.html").read_text()
    for old in OLD_COLOURS:
        assert old not in source


@pytest.mark.parametrize(
    "colour, tint",
    [
        (DEFAULT_COLOUR, DEFAULT_TINT),
        (BRAND_COLOUR, BRAND_TINT),
        ("#000", "#D4D4D4"),
        # The branding row also accepts these; they cannot be mixed, so the pill goes neutral.
        ("#7A00E6CC", "#F3F4F6"),
        ("rgb(122, 0, 230)", "#F3F4F6"),
        ("purple", "#F3F4F6"),
        ("", "#F3F4F6"),
    ],
)
def test_tint(colour, tint):
    assert _tint(colour) == tint
