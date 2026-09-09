"""Savings contract for pod_right_sizing rows.

Requests are per pod, so a workload's saving is the per-pod delta times the
number of running pods; reporting the per-pod figure understated a 20-replica
Deployment twentyfold. Savings are floored at zero only once every container
has been merged, so a workload over-provisioned on one resource and
under-provisioned on another still reports its true net rather than an
inflated per-resource figure.
"""

import json

import pytest

from server.recommendation.vertical_rightsizing import (
    calculate_container_savings,
    finalize_workload_rows,
)

GB = 1024 * 1024 * 1024
CPU_RATE = 0.04  # $ per core-hour
MEM_RATE = 0.005  # $ per GB-hour
HOURS_PER_MONTH = 24 * 30


def _cpu_entry(allocated, recommended):
    return {"resource": "cpu", "allocated": {"request": allocated}, "recommended": {"request": recommended}}


def _memory_entry(allocated, recommended):
    return {"resource": "memory", "allocated": {"request": allocated}, "recommended": {"request": recommended}}


HALF_CORE_SAVED = 0.5 * CPU_RATE * HOURS_PER_MONTH


@pytest.mark.parametrize(
    "name,pods_count,expected",
    [
        ("single_pod", 1, HALF_CORE_SAVED),
        ("twenty_replicas", 20, HALF_CORE_SAVED * 20),
        ("three_replicas", 3, HALF_CORE_SAVED * 3),
        ("zero_pods_floors_at_one", 0, HALF_CORE_SAVED),
        ("negative_pods_floors_at_one", -5, HALF_CORE_SAVED),
    ],
)
def test_cpu_savings_scale_with_pod_count(name, pods_count, expected):
    content = [_cpu_entry(1.0, 0.5)]
    assert calculate_container_savings(content, CPU_RATE, MEM_RATE, pods_count) == pytest.approx(expected)


def test_pods_count_defaults_to_one():
    content = [_cpu_entry(1.0, 0.5)]
    assert calculate_container_savings(content, CPU_RATE, MEM_RATE) == pytest.approx(HALF_CORE_SAVED)


def test_memory_savings_scale_with_pod_count():
    content = [_memory_entry(4 * GB, 2 * GB)]
    expected = 2 * MEM_RATE * HOURS_PER_MONTH * 3
    assert calculate_container_savings(content, CPU_RATE, MEM_RATE, 3) == pytest.approx(expected)


def test_raising_requests_returns_a_negative_before_the_merge_clamp():
    content = [_cpu_entry(0.5, 1.0)]
    expected = -0.5 * CPU_RATE * HOURS_PER_MONTH * 4
    assert calculate_container_savings(content, CPU_RATE, MEM_RATE, 4) == pytest.approx(expected)


@pytest.mark.parametrize(
    "name,content",
    [
        ("not_enough_data", [{"resource": "cpu", "info": "Not enough data"}]),
        ("no_data", [{"resource": "cpu", "info": "No data"}]),
        ("unset_recommendation", [_cpu_entry(1.0, "?")]),
        ("missing_allocated", [{"resource": "cpu", "recommended": {"request": 0.5}}]),
    ],
)
def test_rows_without_usable_numbers_contribute_nothing(name, content):
    assert calculate_container_savings(content, CPU_RATE, MEM_RATE, 20) == 0.0


def _row(savings):
    payload = json.dumps({"app": [_cpu_entry(1.0, 0.5)]})
    return {"estimated_savings": savings, "recommendation": payload}


@pytest.mark.parametrize(
    "name,savings,expected",
    [
        ("positive_total_preserved", 120.0, 120.0),
        ("negative_total_clamped", -45.0, 0.0),
        ("zero_stays_zero", 0.0, 0.0),
    ],
)
def test_finalize_floors_merged_savings_at_zero(name, savings, expected):
    rows = {"res-1": _row(savings)}
    # Priority 2 is OK rather than GOOD, so the row is rated, not dropped.
    finalize_workload_rows(rows, {"res-1": [2]})
    assert rows["res-1"]["estimated_savings"] == pytest.approx(expected)


def test_clamp_does_not_disturb_the_no_change_drop():
    rows = {"keep": _row(-10.0), "drop": _row(-10.0)}
    # GOOD priority on every container marks a workload as needing no change.
    dropped = finalize_workload_rows(rows, {"keep": [2], "drop": [1]})
    assert dropped == 1
    assert "drop" not in rows
    assert rows["keep"]["estimated_savings"] == 0.0
