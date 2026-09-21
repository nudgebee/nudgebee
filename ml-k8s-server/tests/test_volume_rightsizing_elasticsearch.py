"""PVC rightsizing against an Elastic Agent Elasticsearch.

Volume rightsizing used to bail out for ES accounts with "not supported", on the belief
that the `volume` metricset keys usage by the pod's volume name and so could not be tied
to a claim. It does carry `kubernetes.persistentvolumeclaim.name` — but only on
PVC-backed volumes, which is what makes it usable as the selector as well as the key.
"""

import asyncio

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from server.recommendation.volume_rightsizing import (
    ES_MAX_NAMESPACES,
    ES_PVC_FIELD,
    VolumeRightsizingService,
)


def _service():
    with patch("server.recommendation.volume_rightsizing.DatabaseEngine.get_engine", return_value=MagicMock()):
        return VolumeRightsizingService(
            account_id="acct",
            tenant_id="tenant",
            metrics_provider="ES",
            elasticsearch={"url": "https://es:9200", "auth_type": "basic", "username": "u", "password": "p"},
        )


def _agg_response(entries):
    """entries: [(namespace, pvc, used_bytes, capacity_bytes)]"""
    by_ns: dict = {}
    for ns, pvc, used, cap in entries:
        by_ns.setdefault(ns, []).append({"key": pvc, "used": {"value": used}, "capacity": {"value": cap}})
    return {
        "aggregations": {
            "ns": {
                "sum_other_doc_count": 0,
                "buckets": [{"key": ns, "pvc": {"buckets": b}} for ns, b in by_ns.items()],
            }
        }
    }


def test_query_requires_a_claim_name_so_non_pvc_mounts_drop_out():
    """configMap/projected/emptyDir mounts report the NODE filesystem size. Summed in,
    they make a small claim look enormous — the exists filter is what excludes them."""
    svc = _service()
    body = svc._es_volume_body(10, None)

    assert {"exists": {"field": ES_PVC_FIELD}} in body["query"]["bool"]["filter"]
    assert body["aggs"]["ns"]["aggs"]["pvc"]["terms"]["field"] == ES_PVC_FIELD
    assert body["size"] == 0


def test_namespace_filter_is_pushed_into_the_query():
    svc = _service()
    body = svc._es_volume_body(10, "loki")
    terms = [f for f in body["query"]["bool"]["filter"] if "term" in f]
    assert {"term": {"kubernetes.namespace": "loki"}} in terms

    # Unfiltered asks must not carry an empty term, which would match nothing.
    unfiltered = svc._es_volume_body(10, None)
    assert not [f for f in unfiltered["query"]["bool"]["filter"] if "term" in f]


def test_usage_maps_are_keyed_to_match_the_pvc_metadata_join():
    svc = _service()
    used, capacity = svc._build_es_usage_maps(
        _agg_response([("loki", "storage-loki-0", 7_000_000_000, 105_492_467_712)])
    )
    assert used["storage-loki-0_loki"] == 7_000_000_000
    assert capacity["storage-loki-0_loki"] == 105_492_467_712


def test_missing_aggregation_values_are_skipped_not_zeroed():
    """A claim with no value must be absent, not present as 0 — a zero capacity would
    propose shrinking the volume to nothing."""
    svc = _service()
    used, capacity = svc._build_es_usage_maps(
        {
            "aggregations": {
                "ns": {
                    "buckets": [
                        {
                            "key": "ns",
                            "pvc": {"buckets": [{"key": "c", "used": {"value": None}, "capacity": {"value": None}}]},
                        }
                    ]
                }
            }
        }
    )
    assert used == {}
    assert capacity == {}


def test_empty_or_null_response_does_not_raise():
    svc = _service()
    assert svc._build_es_usage_maps({}) == ({}, {})
    assert svc._build_es_usage_maps({"aggregations": None}) == ({}, {})


def test_truncated_namespace_aggregation_is_logged(caplog):
    """A capped terms agg drops whole namespaces, which reads as 'those claims have no
    usage' rather than as missing data."""
    svc = _service()
    resp = _agg_response([("loki", "storage-loki-0", 1, 2)])
    resp["aggregations"]["ns"]["sum_other_doc_count"] = 4321
    with caplog.at_level("WARNING"):
        svc._build_es_usage_maps(resp)
    assert "truncated" in caplog.text
    assert str(ES_MAX_NAMESPACES) in caplog.text


def test_claims_without_a_capacity_document_are_skipped():
    """An unmounted claim, or one on a node the agent does not cover, has no volume
    document. Recommending against a zero capacity would shrink every such claim."""
    svc = _service()
    svc._get_pvc_metadata = AsyncMock(
        return_value={
            "loki/storage-loki-0": {
                "name": "storage-loki-0",
                "namespace": "loki",
                "pv_name": "pv-1",
                "storage_class": "sc",
            },
            "loki/orphan": {"name": "orphan", "namespace": "loki", "pv_name": "pv-2", "storage_class": "sc"},
        }
    )
    resp = _agg_response([("loki", "storage-loki-0", 7_000_000_000, 105_492_467_712)])

    with patch("server.recommendation.volume_rightsizing.ElasticsearchTransport") as transport_cls:
        transport_cls.from_config.return_value.async_search = AsyncMock(return_value=resp)
        data = asyncio.run(svc._get_volume_usage_data_elasticsearch(None, MagicMock(), MagicMock()))

    assert [d.pvc_name for d in data] == ["storage-loki-0"]
    assert data[0].capacity_gb == pytest.approx(105_492_467_712 / (1024**3))
    assert data[0].current_usage_gb == pytest.approx(7_000_000_000 / (1024**3))


def test_malformed_buckets_are_skipped_not_fatal():
    """One unreadable bucket must not cost every other claim its recommendation."""
    svc = _service()
    used, capacity = svc._build_es_usage_maps(
        {
            "aggregations": {
                "ns": {
                    "buckets": [
                        "not-a-dict",
                        {"key": None, "pvc": {"buckets": [{"key": "x"}]}},
                        {
                            "key": "loki",
                            "pvc": {
                                "buckets": [
                                    "also-not-a-dict",
                                    {"key": None},
                                    {"key": "bad", "used": {"value": "NaN-ish"}, "capacity": {"value": None}},
                                    {"key": "storage-loki-0", "used": {"value": 7}, "capacity": {"value": 98}},
                                ]
                            },
                        },
                    ]
                }
            }
        }
    )
    assert used == {"storage-loki-0_loki": 7.0}
    assert capacity == {"storage-loki-0_loki": 98.0}
