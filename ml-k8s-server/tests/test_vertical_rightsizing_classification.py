"""Classification contract for pod_right_sizing categories.

A workload where NO container has any cpu/memory request set is a
best-practice finding and lands under Configuration; any set request keeps
the row under RightSizing. The predicate must run on the MERGED workload
payload — never per container — and must stay in sync with its twin in
collector-server/k8s-collector (same table below) and the backfill
migration's SQL predicate.
"""

import pytest

from server.recommendation.vertical_rightsizing import (
    calculate_container_savings,
    classify_pod_right_sizing_category,
)


def _entry(request):
    return {"resource": "cpu", "allocated": {"request": request, "limit": request}, "recommended": {"request": 0.1}}


CASES = [
    ("all_unset_single", {"app": [_entry(None)]}, "Configuration"),
    ("all_unset_multi_container", {"a": [_entry(None)], "b": [_entry(None)]}, "Configuration"),
    ("zero_request_is_unset", {"app": [_entry(0)]}, "Configuration"),
    ("question_mark_is_unset", {"app": [_entry("?")]}, "Configuration"),
    ("bool_is_not_a_request", {"app": [_entry(True)]}, "Configuration"),
    ("missing_allocated_key", {"app": [{"resource": "cpu", "recommended": {"request": 0.1}}]}, "Configuration"),
    ("mixed_stays", {"a": [_entry(0.5)], "b": [_entry(None)]}, "RightSizing"),
    ("mixed_stays_reversed_order", {"b": [_entry(None)], "a": [_entry(0.5)]}, "RightSizing"),
    ("mixed_within_container", {"a": [_entry(None), _entry(0.5)]}, "RightSizing"),
    ("all_set", {"a": [_entry(2), _entry(1)]}, "RightSizing"),
    ("float_request_set", {"a": [_entry(0.001)]}, "RightSizing"),
    ("empty_payload", {}, "RightSizing"),
    ("entries_not_a_list", {"a": {"resource": "cpu"}}, "RightSizing"),
    ("entries_empty_list", {"a": []}, "RightSizing"),
    ("non_list_container_ignored", {"bad": {"resource": "cpu"}, "good": [_entry(None)]}, "Configuration"),
    ("payload_is_a_list", [_entry(None)], "RightSizing"),
    ("payload_is_scalar", "oops", "RightSizing"),
    ("payload_is_none", None, "RightSizing"),
]


@pytest.mark.parametrize("name,payload,expected", CASES, ids=[c[0] for c in CASES])
def test_classify_pod_right_sizing_category(name, payload, expected):
    assert classify_pod_right_sizing_category(payload) == expected


def test_fully_unset_implies_zero_savings():
    """The invariant the migration dry-run leans on: every container the
    classifier treats as unset is also skipped by the savings helper, so a
    fully-unset workload always carries estimated_savings == 0."""
    content = [
        {"resource": "cpu", "allocated": {"request": None}, "recommended": {"request": 0.5}},
        {"resource": "memory", "allocated": {"request": 0}, "recommended": {"request": 1073741824}},
    ]
    assert calculate_container_savings(content, cpu_cost_per_hour=0.05, memory_cost_per_hour=0.01) == 0.0
