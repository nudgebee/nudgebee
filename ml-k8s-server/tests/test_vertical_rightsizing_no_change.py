"""No-change contract for pod_right_sizing rows.

A workload whose every container scanned GOOD already matches its recommended
requests, so it carries no action and must not be written. The predicate runs
per workload, never per container — one right-sized container says nothing
about its siblings.

The priority test alone is not enough: a scan can flag a workload as
over-provisioned (WARNING) and then clamp its recommendation back to the
allocated floor, landing on the same numbers it started from. Those rows advise
changing a value to itself, so a second, value-based predicate compares every
recommended request against its allocated request on the merged payload.

Suppressed workloads must also leave the archive keep-set, or the rows already
stored for them stay Open forever. That is why the keep-set (what stays Open)
is passed separately from the scanned set (what the scan covered).
"""

import json

import pytest

from server.recommendation.vertical_rightsizing import (
    GOOD_PRIORITY,
    SEVERITY_WEIGHT,
    finalize_workload_rows,
    get_severity,
    is_no_change_workload,
    is_value_no_change_workload,
    worst_priority,
)

CRITICAL, WARNING, OK, GOOD, UNKNOWN = 4, 3, 2, 1, 0

# Severity rank, worst first — deliberately NOT the priority order: Low (0)
# outranks Info (1). Kept here so the tests assert the intent independently.
SEVERITY_ORDER = ["Critical", "High", "Medium", "Low", "Info"]

CASES = [
    ("single_good_container", [GOOD], True),
    ("every_container_good", [GOOD, GOOD, GOOD], True),
    ("one_container_critical", [GOOD, CRITICAL], False),
    ("one_container_warning", [GOOD, WARNING], False),
    ("one_container_ok", [GOOD, OK], False),
    ("one_container_unknown", [GOOD, UNKNOWN], False),
    ("unknown_alone_is_not_no_change", [UNKNOWN], False),
    ("all_critical", [CRITICAL, CRITICAL], False),
    ("no_containers_scanned", [], False),
]


@pytest.mark.parametrize("name,priorities,expected", CASES, ids=[c[0] for c in CASES])
def test_is_no_change_workload(name, priorities, expected):
    assert is_no_change_workload(priorities) is expected


def test_good_priority_tracks_the_enum():
    assert GOOD_PRIORITY == GOOD


def test_info_is_unreachable_once_no_change_workloads_are_dropped():
    """Info is the severity a fully-GOOD scan produces, and those are exactly the
    workloads that get dropped — so no retained workload can carry it."""
    assert get_severity(GOOD_PRIORITY) == "Info"
    reachable = {
        get_severity(worst_priority(prios))
        for prios in [[c] for c in (CRITICAL, WARNING, OK, GOOD, UNKNOWN)]
        + [[GOOD, UNKNOWN], [GOOD, OK], [UNKNOWN, WARNING], [GOOD, GOOD]]
        if not is_no_change_workload(prios)
    }
    assert "Info" not in reachable


WORST_CASES = [
    ("single_container", [WARNING], "High"),
    ("worst_wins_over_good", [GOOD, CRITICAL, OK], "Critical"),
    ("order_does_not_matter", [OK, CRITICAL, GOOD], "Critical"),
    # priority 0 < 1 numerically, but Low outranks Info — a container we could
    # not compute must not be masked by a right-sized sibling.
    ("unknown_outranks_good", [GOOD, UNKNOWN], "Low"),
    ("unknown_outranks_good_reversed", [UNKNOWN, GOOD], "Low"),
    ("real_finding_outranks_unknown", [UNKNOWN, WARNING], "High"),
    ("all_good", [GOOD, GOOD], "Info"),
]


@pytest.mark.parametrize("name,priorities,expected", WORST_CASES, ids=[c[0] for c in WORST_CASES])
def test_workload_severity_is_the_worst_container_not_the_highest_number(name, priorities, expected):
    assert get_severity(worst_priority(priorities)) == expected
    assert get_severity(worst_priority(list(reversed(priorities)))) == expected


def test_severity_weight_covers_every_label_get_severity_can_return():
    """worst_priority indexes SEVERITY_WEIGHT directly, so a label missing from it
    would raise rather than mis-rank."""
    assert {get_severity(p) for p in range(-1, 6)} <= set(SEVERITY_WEIGHT)


def test_severity_weight_ranks_low_above_info():
    """The ordering worst_priority relies on, asserted independently of the values."""
    assert sorted(SEVERITY_ORDER, key=lambda label: -SEVERITY_WEIGHT[label]) == SEVERITY_ORDER


def test_worst_priority_tolerates_values_off_the_scale():
    """get_severity's inequality ladder keeps the ranking key total, so an
    unexpected priority cannot crash the aggregation."""
    assert get_severity(worst_priority([9, GOOD])) == "Critical"
    assert get_severity(worst_priority([-3, GOOD])) == "Low"


MIB = 1024 * 1024


def _entry(resource, allocated, recommended, allocated_limit=None, recommended_limit=None):
    return {
        "resource": resource,
        "allocated": {"request": allocated, "limit": allocated_limit},
        "recommended": {"request": recommended, "limit": recommended_limit},
    }


# The production shape this predicate exists for: requests already at the
# 10m/100Mi floors, recommendation clamped back onto them.
CLAMPED_TO_FLOOR = {"app": [_entry("cpu", 0.01, 0.01), _entry("memory", 100 * MIB, 100 * MIB)]}

VALUE_CASES = [
    ("clamped_to_allocated_floor", CLAMPED_TO_FLOOR, True),
    ("cpu_differs", {"app": [_entry("cpu", 0.02, 0.01), _entry("memory", 100 * MIB, 100 * MIB)]}, False),
    ("memory_differs", {"app": [_entry("cpu", 0.01, 0.01), _entry("memory", 64 * MIB, 100 * MIB)]}, False),
    # Requests only: the strategy leaves the CPU limit unset and derives the
    # memory limit, and the UI renders request changes only — differing limits
    # must not keep a row that displays as '='.
    (
        "differing_limits_do_not_block",
        {
            "app": [
                _entry("cpu", 0.01, 0.01, allocated_limit=0.1),
                _entry("memory", 100 * MIB, 100 * MIB, recommended_limit=150 * MIB),
            ]
        },
        True,
    ),
    # An unset allocated request is a real change (set it) — the Configuration rows.
    ("unset_allocated_is_a_change", {"app": [_entry("cpu", None, 0.01)]}, False),
    ("zero_allocated_is_a_change", {"app": [_entry("cpu", 0, 0.01)]}, False),
    # Fail-open: a value we cannot read is not proof of nothing-to-do.
    (
        "unreadable_recommended_keeps_the_row",
        {"app": [_entry("cpu", 0.01, "?"), _entry("memory", 100 * MIB, 100 * MIB)]},
        False,
    ),
    ("missing_recommended_keeps_the_row", {"app": [{"resource": "cpu", "allocated": {"request": 0.01}}]}, False),
    ("malformed_entries_keep_the_row", {"app": "not-a-list"}, False),
    ("empty_payload_is_not_no_change", {}, False),
    ("non_dict_payload_is_not_no_change", None, False),
    # Per workload: one clamped container says nothing about its siblings.
    (
        "sibling_with_a_change_keeps_the_workload",
        {"a": [_entry("cpu", 0.01, 0.01)], "b": [_entry("cpu", 0.5, 0.25)]},
        False,
    ),
    (
        "every_sibling_clamped_is_no_change",
        {"a": [_entry("cpu", 0.01, 0.01)], "b": [_entry("memory", 100 * MIB, 100 * MIB)]},
        True,
    ),
]


@pytest.mark.parametrize("name,payload,expected", VALUE_CASES, ids=[c[0] for c in VALUE_CASES])
def test_is_value_no_change_workload(name, payload, expected):
    assert is_value_no_change_workload(payload) is expected


def test_value_equality_survives_a_json_round_trip():
    """The predicate runs on json.loads(row) output, exactly as finalize does."""
    assert is_value_no_change_workload(json.loads(json.dumps(CLAMPED_TO_FLOOR))) is True


def test_finalize_drops_clamped_workloads_regardless_of_priority():
    """A WARNING-priority workload whose numbers equal what is allocated must be
    dropped, so it also leaves the keep-set the caller builds from this dict."""
    rows = {
        "clamped": {"estimated_savings": 0.0, "recommendation": json.dumps(CLAMPED_TO_FLOOR)},
        "real": {
            "estimated_savings": 12.0,
            "recommendation": json.dumps({"app": [_entry("cpu", 0.02, 0.01)]}),
        },
    }
    dropped = finalize_workload_rows(rows, {"clamped": [WARNING], "real": [WARNING]})
    assert dropped == 1
    assert "clamped" not in rows
    assert rows["real"]["severity"] == "High"
