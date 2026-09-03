"""Tests for Prometheus metrics processing and deduplication."""

import json
from rag.core.monitoring.metrics import _process_metrics_list, extract_metric_names


def test_process_metrics_list_empty_and_falsy():
    assert _process_metrics_list([]) == []
    assert _process_metrics_list([None, "", 0, False]) == []


def test_process_metrics_list_strings():
    metrics = ["node_cpu_seconds_total", "http_requests_total", "node_cpu_seconds_total"]
    result = _process_metrics_list(metrics)
    assert set(result) == {"node_cpu_seconds_total", "http_requests_total"}


def test_process_metrics_list_dicts_with_known_keys():
    metrics = [
        {"metric": "node_cpu_seconds_total"},
        {"__name__": "http_requests_total"},
        {"name": "process_resident_memory_bytes"},
    ]
    result = _process_metrics_list(metrics)
    assert set(result) == {
        "node_cpu_seconds_total",
        "http_requests_total",
        "process_resident_memory_bytes",
    }


def test_process_metrics_list_arbitrary_dicts():
    dict1 = {"job": "prometheus", "instance": "localhost:9090"}
    dict2 = {"job": "prometheus", "instance": "localhost:9090"}
    result = _process_metrics_list([dict1, dict2])
    expected = json.dumps(dict1, sort_keys=True)
    assert result == [expected]


def test_process_metrics_list_mixed_types_and_deduplication():
    metrics = [
        "node_cpu_seconds_total",
        {"metric": "node_cpu_seconds_total"},
        {"__name__": "http_requests_total"},
        "http_requests_total",
        {"name": "custom_metric"},
        None,
        "",
    ]
    result = _process_metrics_list(metrics)
    assert set(result) == {
        "node_cpu_seconds_total",
        "http_requests_total",
        "custom_metric",
    }


def test_extract_metric_names():
    documents = [
        {"metadata": {"metric": "node_cpu_seconds_total"}},
        {"metadata": {"metric": "http_requests_total"}},
        {"metadata": {"other": "value"}},
        {"content": "something without metadata"},
        {"metadata": {"metric": "node_cpu_seconds_total"}},
    ]
    result = extract_metric_names(documents)
    assert result == {"node_cpu_seconds_total", "http_requests_total"}
