"""Elasticsearch rightsizing metrics: query shape and response mapping.

An Elastic Agent cluster keeps a metric's identity in its FIELD PATH rather than in a
name/value pair, so every assertion here is about paths and units. Both are the kind of
mistake that produces a plausible-looking number instead of an error: a wrong field
matches nothing and reads as "this workload is idle", and a missing nanocores
conversion is off by a factor of a billion and still renders.
"""

import asyncio
from datetime import timedelta

import pytest

from server.recommendation.vertical_rightsizing.models.allocations import (
    ResourceAllocations,
)
from server.recommendation.vertical_rightsizing.models.objects import (
    K8sObjectData,
    PodData,
)
from server.recommendation.vertical_rightsizing.services.metrics_elasticsearch_service import (
    CONTAINER_CPU_NANOCORES,
    CONTAINER_MEMORY_WORKINGSET,
    ElasticsearchMetricsService,
)


class _Strategy:
    """Only `metrics` is read by gather_data."""

    def __init__(self, metrics):
        self.metrics = metrics


NUDGEBEE_METRIC_KEYS = [
    "cpu_percentile_99",
    "cpu_percentile_97",
    "cpu_percentile_95",
    "cpu_percentile_92",
    "MaxMemoryLoader",
    "MaxUsageMemoryLoader",
    "CPUAmountLoader",
    "MemoryAmountLoader",
    "MaxOOMKilledMemoryLoader",
]


def _service(**kwargs):
    defaults = dict(
        config=None,
        account_id="acct",
        executor=None,
        url="https://es.example:9200/",
        username="elastic",
        password="secret",
        metrics_index="metricbeat-*",
    )
    defaults.update(kwargs)
    return ElasticsearchMetricsService(**defaults)


def _object(pods=("app-abc-1", "app-abc-2")):
    return K8sObjectData(
        cluster=None,
        name="app",
        container="app",
        namespace="ns",
        kind="Deployment",
        hpa=None,
        pods=[PodData(name=p, deleted=False) for p in pods],
        allocations=ResourceAllocations(requests={}, limits={}),
    )


def _capture(service, responses):
    """Record the bodies sent and answer with canned responses, in order."""
    sent = []
    pending = list(responses)

    def fake_search(body):
        sent.append(body)
        return pending.pop(0)

    service._search = fake_search
    return sent


def _pods_agg(buckets):
    return {"aggregations": {"pods": {"sum_other_doc_count": 0, "buckets": buckets}}}


def _filters(body):
    return body["query"]["bool"]["filter"]


def test_query_selects_dataset_by_field_existence_and_scopes_to_workload():
    service = _service()
    sent = _capture(service, [_pods_agg([]), _pods_agg([])])

    asyncio.run(service.gather_data(_object(), _Strategy(NUDGEBEE_METRIC_KEYS), timedelta(days=14)))

    cpu_body, mem_body = sent
    cpu_filters = _filters(cpu_body)
    # The exists clause is what picks the container metricset: no term on
    # metricset.name, which a customer template may map as text.
    assert {"exists": {"field": CONTAINER_CPU_NANOCORES}} in cpu_filters
    assert {"term": {"kubernetes.namespace": "ns"}} in cpu_filters
    assert {"term": {"kubernetes.container.name": "app"}} in cpu_filters
    assert {"terms": {"kubernetes.pod.name": ["app-abc-1", "app-abc-2"]}} in cpu_filters

    assert {"exists": {"field": CONTAINER_MEMORY_WORKINGSET}} in _filters(mem_body)


def test_query_falls_back_to_pod_name_prefix_when_pods_unknown():
    service = _service()
    sent = _capture(service, [_pods_agg([]), _pods_agg([])])

    asyncio.run(service.gather_data(_object(pods=()), _Strategy(["MaxMemoryLoader"]), timedelta(days=1)))

    assert {"prefix": {"kubernetes.pod.name": "app-"}} in _filters(sent[0])


def test_gather_data_converts_nanocores_and_keys_by_pod():
    service = _service()
    _capture(
        service,
        [
            _pods_agg(
                [
                    {
                        "key": "app-abc-1",
                        "count": {"value": 120},
                        "last_seen": {"value": 1787811900000},
                        # Elasticsearch keys percentiles by the percent as a float
                        # string, which does not match the caller's "99".
                        "pct": {"values": {"99.0": 2_500_000_000.0, "95.0": 1_000_000_000.0}},
                    }
                ]
            ),
            _pods_agg(
                [
                    {
                        "key": "app-abc-1",
                        "count": {"value": 118},
                        "last_seen": {"value": 1787811900000},
                        "max": {"value": 536_870_912.0},
                    }
                ]
            ),
        ],
    )

    result = asyncio.run(
        service.gather_data(
            _object(),
            _Strategy(
                [
                    "cpu_percentile_99",
                    "cpu_percentile_95",
                    "MaxMemoryLoader",
                    "CPUAmountLoader",
                ]
            ),
            timedelta(days=14),
        )
    )

    assert result["cpu_percentile_99"]["app-abc-1"][0][1] == pytest.approx(2.5)
    assert result["cpu_percentile_95"]["app-abc-1"][0][1] == pytest.approx(1.0)
    assert result["MaxMemoryLoader"]["app-abc-1"][0][1] == pytest.approx(536_870_912.0)
    assert result["CPUAmountLoader"]["app-abc-1"][0][1] == pytest.approx(120)
    # Stamped with the pod's own last observation, in seconds — the strategy and the
    # Datadog service both work in seconds.
    assert result["cpu_percentile_99"]["app-abc-1"][0][0] == pytest.approx(1787811900.0)


def test_gather_data_requests_only_the_percentiles_the_strategy_asks_for():
    service = _service()
    sent = _capture(service, [_pods_agg([]), _pods_agg([])])

    asyncio.run(
        service.gather_data(
            _object(),
            _Strategy(["cpu_percentile_92", "cpu_percentile_99"]),
            timedelta(days=1),
        )
    )

    percents = sent[0]["aggs"]["pods"]["aggs"]["pct"]["percentiles"]["percents"]
    assert percents == [92.0, 99.0]


def test_unavailable_loaders_return_empty_rather_than_zero():
    """Two loaders have no kubelet-stats equivalent.

    Empty is the contract: MaxUsageMemoryLoader is declared warning_on_no_data = False
    so the limit falls back to the request. A zero would be read as a real reading and
    would floor the limit at nothing.
    """
    service = _service()
    _capture(service, [_pods_agg([]), _pods_agg([])])

    result = asyncio.run(service.gather_data(_object(), _Strategy(NUDGEBEE_METRIC_KEYS), timedelta(days=14)))

    assert result["MaxUsageMemoryLoader"] == {}
    assert result["MaxOOMKilledMemoryLoader"] == {}
    # Every key the strategy asked for is present, so the strategy never sees a
    # missing key it would have to guess about.
    assert set(result) == set(NUDGEBEE_METRIC_KEYS)


def test_gather_data_returns_empty_metrics_when_the_query_fails():
    service = _service()

    def boom(body):
        raise RuntimeError("connection refused")

    service._search = boom

    result = asyncio.run(service.gather_data(_object(), _Strategy(NUDGEBEE_METRIC_KEYS), timedelta(days=14)))
    assert set(result) == set(NUDGEBEE_METRIC_KEYS)
    assert all(v == {} for v in result.values())


def test_load_pods_discovers_pods_without_narrowing_by_a_known_list():
    service = _service()
    sent = _capture(service, [_pods_agg([{"key": "app-abc-9"}, {"key": "app-abc-8"}])])

    pods = asyncio.run(service.load_pods(_object(), timedelta(days=1)))

    assert sorted(p.name for p in pods) == ["app-abc-8", "app-abc-9"]
    # This call is what discovers the pod list, so it must not be filtered by the
    # (possibly stale) one already on the object.
    assert not any("terms" in f for f in _filters(sent[0]))


def test_load_pods_returns_empty_on_failure_so_the_database_fallback_runs():
    service = _service()

    def boom(body):
        raise RuntimeError("timeout")

    service._search = boom
    assert asyncio.run(service.load_pods(_object(), timedelta(days=1))) == []


def test_cognito_is_refused_explicitly():
    # SigV4 signing is Go-only. An unsigned request would come back 403, which the
    # caller reads as "no metrics" — the exact silent failure this work removes.
    with pytest.raises(ValueError, match="cognito"):
        _service(auth_type="cognito")


def test_api_key_and_bearer_auth_set_headers_instead_of_basic_auth():
    api_key_service = _service(auth_type="api_key", api_key="abc123")
    assert api_key_service._headers["Authorization"] == "ApiKey abc123"
    assert api_key_service._auth is None

    bearer_service = _service(auth_type="bearer_token", bearer_token="tok")
    assert bearer_service._headers["Authorization"] == "Bearer tok"
    assert bearer_service._auth is None

    basic_service = _service()
    assert basic_service._auth == ("elastic", "secret")
    assert "Authorization" not in basic_service._headers


def test_missing_url_is_rejected():
    with pytest.raises(ValueError, match="url"):
        _service(url="")
