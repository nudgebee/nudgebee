"""No-change contract for pod_right_sizing rows.

A workload whose every container scanned GOOD already matches its recommended
requests, so it carries no action and must not be written. The predicate runs
per workload, never per container — one right-sized container says nothing
about its siblings.

Suppressed workloads must also leave the archive keep-set, or the rows already
stored for them stay Open forever. That is why the keep-set (what stays Open)
is passed separately from the scanned set (what the scan covered).
"""

import pytest

from server.recommendation.vertical_rightsizing import (
    GOOD_PRIORITY,
    SEVERITY_WEIGHT,
    get_severity,
    is_no_change_workload,
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
