"""Severity contract for pod_right_sizing rows.

get_severity consumes the 0-4 integer from scan_severity_to_priority, not a
normalized 0-1 score. Reading it as a score collapses every non-UNKNOWN scan
into Critical and makes High/Medium unreachable, so the seam between the two
functions is pinned here as well as the mapping itself.

Every label must exist in recommendation_severity_type: the severity column
references it via recommendation_severity_fkey, so an unlisted value (a
lowercase or uppercased variant, say) fails the insert outright.
"""

import pytest

from server.recommendation.vertical_rightsizing import get_severity
from server.recommendation.vertical_rightsizing.models.result import scan_severity_to_priority
from server.recommendation.vertical_rightsizing.models.severity import Severity

FK_VOCABULARY = {"Critical", "High", "Medium", "Low", "Info"}

PRIORITY_CASES = [
    (4, "Critical"),
    (3, "High"),
    (2, "Medium"),
    (1, "Info"),
    (0, "Low"),
]

SCAN_CASES = [
    (Severity.CRITICAL, "Critical"),
    (Severity.WARNING, "High"),
    (Severity.OK, "Medium"),
    (Severity.GOOD, "Info"),
    (Severity.UNKNOWN, "Low"),
]


@pytest.mark.parametrize("priority,expected", PRIORITY_CASES, ids=[str(c[0]) for c in PRIORITY_CASES])
def test_get_severity_maps_each_priority(priority, expected):
    assert get_severity(priority) == expected


@pytest.mark.parametrize("scan_severity,expected", SCAN_CASES, ids=[c[0].value for c in SCAN_CASES])
def test_scan_severity_reaches_the_matching_label(scan_severity, expected):
    assert get_severity(scan_severity_to_priority(scan_severity)) == expected


def test_every_label_is_a_valid_foreign_key():
    assert {get_severity(p) for p, _ in PRIORITY_CASES} <= FK_VOCABULARY


def test_high_and_medium_are_reachable():
    """The regression this guards: 0-1 thresholds left both buckets empty."""
    labels = [get_severity(p) for p, _ in PRIORITY_CASES]
    assert "High" in labels
    assert "Medium" in labels


def test_priority_above_the_scale_stays_critical():
    assert get_severity(5) == "Critical"
