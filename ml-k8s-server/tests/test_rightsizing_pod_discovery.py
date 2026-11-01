"""Pod discovery asks Prometheus for pod names once, not once per hour of the window.

Regression context (#39991): load_pods ran last_over_time(kube_pod_owner[14d]) as a
range query over the same 14 days. Every pod came back at up to 337 steps although only
its name was read. For a DaemonSet on a cluster with churning nodes that answer was
146 MB, and reading it OOM-killed api-server.
"""

import asyncio
from datetime import timedelta

from server.recommendation.vertical_rightsizing.models.allocations import ResourceAllocations
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData
from server.recommendation.vertical_rightsizing.services.metrics_prometheus_service import PrometheusMetricsService


def _object(kind: str):
    return K8sObjectData(
        cluster=None,
        name="node-agent",
        container="node-agent",
        namespace="agent",
        kind=kind,
        hpa=None,
        allocations=ResourceAllocations(requests={}, limits={}),
    )


class _Service(PrometheusMetricsService):
    """The real load_pods over a recorded Prometheus."""

    def __init__(self, pods, live, replicasets=()):
        # Deliberately not calling super().__init__: it builds an api-server client.
        self.instant_queries = []
        self.range_queries = []
        self._pods = pods
        self._live = live
        self._replicasets = replicasets

    def get_prometheus_cluster_label(self) -> str:
        return ""

    async def query(self, query: str):
        self.instant_queries.append(query)
        if "kube_pod_owner" in query:
            return [{"metric": {"pod": pod}, "value": [1_790_000_000, "1"]} for pod in self._pods]
        return [{"metric": {"pod": pod}, "value": [1_790_000_000, "1"]} for pod in self._live]

    async def query_range(self, query: str, start, end, step):
        self.range_queries.append(query)
        return [{"metric": {"replicaset": rs}, "values": []} for rs in self._replicasets]


def test_pods_are_discovered_with_one_instant_query():
    service = _Service(pods=["node-agent-a", "node-agent-b", "node-agent-gone"], live=["node-agent-a", "node-agent-b"])

    pods = asyncio.run(service.load_pods(_object("DaemonSet"), timedelta(days=14)))

    assert service.range_queries == [], "pod names need no range query"
    owner_queries = [q for q in service.instant_queries if "kube_pod_owner" in q]
    assert len(owner_queries) == 1
    # One row per pod over the whole window, and nothing but the pod label.
    assert "count by (pod)" in owner_queries[0]
    assert "[14d]" in owner_queries[0]
    assert 'owner_kind="DaemonSet"' in owner_queries[0]
    assert {p.name: p.deleted for p in pods} == {
        "node-agent-a": False,
        "node-agent-b": False,
        "node-agent-gone": True,
    }


def test_deployment_pods_are_looked_up_through_its_replicasets():
    service = _Service(pods=["web-5d9-x"], live=["web-5d9-x"], replicasets=["web-5d9", "web-7c1"])

    pods = asyncio.run(service.load_pods(_object("Deployment"), timedelta(days=14)))

    # The ReplicaSet walk is unchanged; only the pod lookup stopped being a range query.
    assert len(service.range_queries) == 1 and "kube_replicaset_owner" in service.range_queries[0]
    owner_query = next(q for q in service.instant_queries if "kube_pod_owner" in q)
    assert 'owner_kind="ReplicaSet"' in owner_query
    assert "web-5d9" in owner_query and "web-7c1" in owner_query
    assert [p.name for p in pods] == ["web-5d9-x"]
