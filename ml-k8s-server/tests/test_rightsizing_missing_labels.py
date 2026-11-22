"""A Prometheus series that lacks a label must be skipped, not crash rightsizing.

Regression context (#39942): lookups like series["metric"]["pod"] raised KeyError when a
series came back without a "pod" label, which failed the whole vertical rightsizing run
for that workload.
"""

import asyncio
from datetime import timedelta

import numpy as np

from server.metrics.prometheus_metrics import Prometheus
from server.recommendation.vertical_rightsizing.metrics.base import PrometheusMetric
from server.recommendation.vertical_rightsizing.models.allocations import ResourceAllocations
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData
from server.recommendation.vertical_rightsizing.services.metrics_prometheus_service import PrometheusMetricsService
from server.utils.utils import safe_get


def _object(kind: str = "Deployment"):
    return K8sObjectData(
        cluster=None,
        name="web",
        container="web",
        namespace="shop",
        kind=kind,
        hpa=None,
        allocations=ResourceAllocations(requests={}, limits={}),
    )


def test_safe_get_returns_the_default_for_any_missing_step():
    series = {"metric": {"pod": "web-1"}, "values": [[1, "2"]]}

    assert safe_get(series, ["metric", "pod"]) == "web-1"
    assert safe_get(series, ["metric", "container"]) is None
    assert safe_get(series, ["metric", "container"], "none") == "none"
    assert safe_get(series, ["nope", "pod"]) is None
    assert safe_get({"metric": None}, ["metric", "pod"]) is None
    assert safe_get({"metric": "not-a-dict"}, ["metric", "pod"]) is None
    assert safe_get(None, ["metric"]) is None
    assert safe_get(series, "metric") == {"pod": "web-1"}


def test_target_name_and_job_filter_survive_a_series_without_metric_labels():
    labelless = {"values": [[1, "1"]]}
    kubelet = {"metric": {"pod": "web-1", "job": "kubelet"}, "values": [[1, "1"]]}

    assert PrometheusMetric.get_target_name(labelless) is None
    assert PrometheusMetric.get_target_name(kubelet) == "web-1"
    assert PrometheusMetric.filter_prom_jobs_results([labelless, kubelet]) == [kubelet]


class _Metric(PrometheusMetric):
    """The real load_data over a recorded Prometheus answer."""

    pods_batch_size = None
    filtering = False

    def __init__(self, series):
        # Deliberately not calling super().__init__: it expects a live Prometheus client.
        self._series = series

    def get_query(self, object, duration, step):
        return "q"

    async def query_prometheus(self, data):
        return self._series


def test_load_data_skips_series_without_a_pod_label_or_without_values():
    series = [
        {"metric": {"pod": "web-1"}, "values": [[1, "1"], [2, "3"]]},
        {"metric": {"container": "web"}, "values": [[1, "9"]]},
        {"metric": {"pod": "web-2"}},
        {"values": [[1, "9"]]},
    ]

    data = asyncio.run(_Metric(series).load_data(_object(), timedelta(hours=1), timedelta(minutes=5)))

    assert list(data) == ["web-1"]
    assert data["web-1"].tolist() == np.array([[1, 1], [2, 3]], dtype=np.float64).tolist()


def test_custom_query_drops_malformed_rows_instead_of_raising_or_returning_none():
    prometheus = Prometheus.__new__(Prometheus)
    prometheus.query_metrics = lambda *args, **kwargs: [
        {"metric": {"pod": "web-1"}, "value": {"timestamp": 10, "value": "5"}},
        {"metric": {"pod": "web-2"}},
        {"metric": {"pod": "web-3"}, "value": {"timestamp": 10}},
        "not-a-series",
    ]

    rows = prometheus.custom_query("up")

    assert rows == [{"metric": {"pod": "web-1"}, "value": [10, "5"]}]


class _Service(PrometheusMetricsService):
    """The real load_pods over a Prometheus whose series are missing labels."""

    def __init__(self, replicasets, owned_pods, live_pods):
        # Deliberately not calling super().__init__: it builds an api-server client.
        self._replicasets = replicasets
        self._owned_pods = owned_pods
        self._live_pods = live_pods

    def get_prometheus_cluster_label(self) -> str:
        return ""

    async def query(self, query: str):
        pods = self._owned_pods if "kube_pod_owner" in query else self._live_pods
        return pods

    async def query_range(self, query: str, start, end, step):
        return self._replicasets


def test_load_pods_ignores_series_that_lack_the_owner_or_pod_label():
    service = _Service(
        replicasets=[{"metric": {"replicaset": "web-5d9"}, "values": []}, {"metric": {}, "values": []}],
        owned_pods=[{"metric": {"pod": "web-5d9-x"}, "value": [1, "1"]}, {"metric": {}, "value": [1, "1"]}],
        live_pods=[{"metric": {"pod": "web-5d9-x"}, "value": [1, "1"]}, {"metric": {}, "value": [1, "1"]}],
    )

    pods = asyncio.run(service.load_pods(_object("Deployment"), timedelta(days=1)))

    assert {p.name: p.deleted for p in pods} == {"web-5d9-x": False}
