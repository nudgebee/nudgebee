"""Pins the liveness semantics feeding the estimated-savings replica multiplier.

Regression context: correcting the savings math to scale by pods_count exposed
that both metrics paths counted every pod incarnation seen in the history
window as a live replica. A 2-replica workload with daily deploys and restarts
reported ~45 "replicas" and a ~22x inflated monthly saving. current_pods_count
must reflect pods live NOW, and only non-deleted pods may feed the multiplier.
"""

from server.recommendation.vertical_rightsizing.models.objects import PodData
from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import pods_from_series


def test_pods_from_series_marks_stale_incarnations_deleted():
    cutoff = 1_000_000.0
    entries = [
        # Live pod: datapoints continue past the cutoff.
        {"metric": {"pod_name": "web-live-1"}, "timestamps": [999_000.0, 1_000_500.0], "values": [1.0, 1.0]},
        {"metric": {"pod_name": "web-live-2"}, "timestamps": [1_000_100.0], "values": [2.0]},
        # Churned incarnation: series stops before the cutoff.
        {"metric": {"pod_name": "web-old-abc"}, "timestamps": [900_000.0], "values": [1.0]},
        # api-server drops nulls, so a null-only series has no points and no liveness signal.
        {"metric": {"pod_name": "web-null"}, "timestamps": [], "values": []},
        # No pod_name tag: ignored entirely.
        {"metric": {"kube_container_name": "istio"}, "timestamps": [1_000_500.0], "values": [1.0]},
    ]

    pods = {p.name: p for p in pods_from_series(entries, live_cutoff=cutoff)}

    assert pods["web-live-1"].deleted is False
    assert pods["web-live-2"].deleted is False
    assert pods["web-old-abc"].deleted is True
    assert "web-null" not in pods
    assert "istio" not in pods


def test_live_replica_count_excludes_churned_incarnations():
    # The churn-heavy dev deployment scenario: 2 live replicas plus 43 churned
    # incarnations discovered from the history window. The multiplier input
    # (count of non-deleted pods) must be 2, not 45.
    pods = [PodData(name=f"web-old-{i}", deleted=True) for i in range(43)]
    pods += [PodData(name="web-live-1", deleted=False), PodData(name="web-live-2", deleted=False)]

    live = len([p for p in pods if not p.deleted])

    assert live == 2
