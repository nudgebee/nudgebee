"""Tests for Prometheus metrics processing and deduplication."""

import json
from rag.core.monitoring.metrics import _process_metrics_list, extract_metric_names, parse_json_string


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


def test_extract_metric_names_reads_the_name_out_of_a_nested_metric_object():
    # Shape found in dev Qdrant: an LLM-generated example whose metric is the
    # whole metadata object instead of the name. This used to raise
    # "unhashable type: 'dict'" and abort the prometheus load for every account.
    documents = [
        {"metadata": {"metric": {"name": "process_major_pagefaults_total", "description": "...", "labels": []}}},
        {"metadata": {"metric": "node_cpu_seconds_total"}},
    ]
    assert extract_metric_names(documents) == {"process_major_pagefaults_total", "node_cpu_seconds_total"}


def test_extract_metric_names_skips_entries_without_a_usable_name():
    documents = [
        {"metadata": {"metric": {"description": "no name key"}}},
        {"metadata": {"metric": None}},
        {"metadata": {"metric": "   "}},
        {"metadata": "not a dict"},
        "not a dict at all",
    ]
    assert extract_metric_names(documents) == set()


def test_process_metrics_list_dict_with_non_string_name_falls_back_to_serialised_dict():
    metric = {"name": {"nested": "object"}}
    assert _process_metrics_list([metric]) == [json.dumps(metric, sort_keys=True)]


def test_parse_json_string_normalises_a_nested_name_to_the_metric_string():
    response = json.dumps(
        [{"question": "q", "answer": "a", "metadata": {"name": {"name": "up", "description": "d", "labels": []}}}]
    )
    parsed = parse_json_string(response)
    assert parsed[0]["metadata"] == {"metric": "up"}


def test_parse_json_string_drops_an_unusable_name():
    response = json.dumps([{"question": "q", "answer": "a", "metadata": {"name": {"description": "only"}}}])
    parsed = parse_json_string(response)
    assert parsed[0]["metadata"] == {"metric": None}
