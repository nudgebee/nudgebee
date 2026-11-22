"""The agent status email does not report Karpenter down on clusters that never had it.

Most clusters don't run Karpenter, and the agent reports karpenterEnabled=false on
all of them. Karpenter only counts as a connection where the cluster runs it: the
agent detected it, or reports it as the autoscaler.
"""

import pytest

from notifications_server.emailer.generator import render_template_html

HEALTHY = {
    "alertManagerConnection": True,
    "grafanaEnabled": True,
    "logsConnection": True,
    "opencostConnection": True,
}


def _render(connection_status):
    params = {
        "title": "Agent Status Report",
        "agent_total": 1,
        "agent_disconnected": 0,
        "agent_partial": 0,
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
                                "connection_status": {**HEALTHY, **connection_status},
                            }
                        ]
                    }
                },
            }
        ],
    }
    return render_template_html(params, "agent_status_report")


@pytest.mark.parametrize(
    "connection_status",
    [
        {},
        {"karpenterEnabled": False},
        {"karpenterEnabled": False, "autoScalerType": "cluster-autoscaler"},
    ],
)
def test_cluster_without_karpenter_is_healthy(connection_status):
    html = _render(connection_status)

    assert "4/4" in html
    assert "Fix these connections" not in html
    assert "Karpenter" not in html


def test_cluster_with_karpenter_counts_it():
    html = _render({"karpenterEnabled": True, "autoScalerType": "karpenter"})

    assert "5/5" in html
    assert "Fix these connections" not in html


def test_karpenter_autoscaler_not_connected_is_reported():
    html = _render({"karpenterEnabled": False, "autoScalerType": "karpenter"})

    assert "4/5" in html
    assert "Fix these connections" in html
    assert "Disconnected: Karpenter" in html
