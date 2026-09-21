"""No-change contract for pod_right_sizing rows on the KRR-upload path.

A row whose every recommended request equals its allocated request advises
changing a value to itself and must not be written. The per-container threshold
ignore in process_resource_recommendation only fires when a container has BOTH
cpu and memory requests set, so the merged-payload value predicate is what
catches single-request workloads. It must stay in sync with its twin in
ml-k8s-server (test_vertical_rightsizing_no_change.py mirrors these cases).

Dropped workloads retire their stored rows for free: the caller archives every
Open pod_right_sizing row for the account and re-inserts only what survives.

Runs offline: heavy deps are stubbed before importing the handler, same as
test_pod_rightsizing_classification.py.
"""

import os
import sys
import unittest
from unittest import mock

_ENV_DEFAULTS = {
    "ENV": "DEV",
    "COLLECTOR_MODE": "worker",
    "COLLECTOR_DB_URL": "postgresql://u:p@localhost:5432/db?sslmode=disable",
    "NUDGEBEE_ENCRYPTION_KEY": "test-key",
    "ACTION_API_SERVER_TOKEN": "",
    "SERVICE_API_SERVER_URL": "http://localhost:8000",
    "RABBIT_MQ_HOST": "localhost",
    "RABBIT_MQ_PORT": "5672",
    "RABBIT_MQ_USERNAME": "guest",
    "RABBIT_MQ_PASSWORD": "guest",
    "REDIS_SERVER_HOST": "localhost",
    "REDIS_SERVER_PORT": "6379",
    "REDIS_USER_NAME": "",
    "REDIS_USER_PASSWORD": "",
    "CLICKHOUSE_ENABLED": "false",
    "CLICKHOUSE_HOST": "",
    "CLICKHOUSE_USER": "",
    "CLICKHOUSE_PASSWORD": "",
}
for _k, _v in _ENV_DEFAULTS.items():
    os.environ.setdefault(_k, _v)

for _mod in ("redis", "psycopg2", "psycopg2.extras", "psycopg2.pool", "clickhouse_driver"):
    sys.modules.setdefault(_mod, mock.MagicMock())

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

from handlers.event_handler import (  # noqa: E402
    is_value_no_change_workload,
    process_resource_recommendation,
)

MIB = 1024 * 1024


def _entry(resource, allocated, recommended):
    return {
        "resource": resource,
        "allocated": {"request": allocated, "limit": allocated},
        "recommended": {"request": recommended, "limit": recommended},
    }


CASES = [
    ("every_request_unchanged", {"app": [_entry("cpu", 0.01, 0.01), _entry("memory", 100 * MIB, 100 * MIB)]}, True),
    # The gap this predicate exists for: only one resource request set.
    ("single_cpu_request_unchanged", {"app": [_entry("cpu", 0.01, 0.01)]}, True),
    ("single_memory_request_unchanged", {"app": [_entry("memory", 100 * MIB, 100 * MIB)]}, True),
    ("cpu_differs", {"app": [_entry("cpu", 0.02, 0.01), _entry("memory", 100 * MIB, 100 * MIB)]}, False),
    ("memory_differs", {"app": [_entry("cpu", 0.01, 0.01), _entry("memory", 64 * MIB, 100 * MIB)]}, False),
    # An unset allocated request is a real change (set it) — the Configuration rows.
    ("unset_allocated_is_a_change", {"app": [_entry("cpu", None, 0.01)]}, False),
    ("zero_allocated_is_a_change", {"app": [_entry("cpu", 0, 0.01)]}, False),
    # Fail-open: a value we cannot read is not proof of nothing-to-do.
    ("unreadable_recommended_keeps_the_row", {"app": [_entry("cpu", 0.01, "?")]}, False),
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

# Empty settings fall back to the 10% change threshold, which clamps a 0% delta
# and leaves a 50% one alone — exactly what these scenarios rely on.
SETTINGS = {}


class TestIsValueNoChangeWorkload(unittest.TestCase):
    def test_cases(self):
        for name, payload, expected in CASES:
            with self.subTest(name):
                self.assertIs(is_value_no_change_workload(payload), expected)


class TestSingleRequestGap(unittest.TestCase):
    """The exact scenario from the ticket: the threshold ignore misses these,
    the value predicate catches them after processing."""

    def test_single_cpu_request_escapes_threshold_ignore_but_is_caught(self):
        content = [_entry("cpu", 0.01, 0.01)]
        _, ignore = process_resource_recommendation(0.04, 0.005, content, SETTINGS, "res-1")
        self.assertFalse(ignore)
        self.assertTrue(is_value_no_change_workload({"app": content}))

    def test_single_memory_request_escapes_threshold_ignore_but_is_caught(self):
        content = [_entry("memory", 100 * MIB, 100 * MIB)]
        _, ignore = process_resource_recommendation(0.04, 0.005, content, SETTINGS, "res-1")
        self.assertFalse(ignore)
        self.assertTrue(is_value_no_change_workload({"app": content}))

    def test_both_requests_unchanged_still_hits_the_existing_ignore(self):
        content = [_entry("cpu", 0.01, 0.01), _entry("memory", 100 * MIB, 100 * MIB)]
        _, ignore = process_resource_recommendation(0.04, 0.005, content, SETTINGS, "res-1")
        self.assertTrue(ignore)

    def test_question_mark_recommendation_is_rewritten_and_caught(self):
        # process_resource_recommendation substitutes "?" with the allocated
        # value, so the stored payload genuinely advises no change.
        content = [_entry("cpu", 0.01, "?")]
        _, ignore = process_resource_recommendation(0.04, 0.005, content, SETTINGS, "res-1")
        self.assertFalse(ignore)
        self.assertTrue(is_value_no_change_workload({"app": content}))

    def test_real_change_survives_processing(self):
        content = [_entry("cpu", 0.5, 0.25)]
        _, ignore = process_resource_recommendation(0.04, 0.005, content, SETTINGS, "res-1")
        self.assertFalse(ignore)
        self.assertFalse(is_value_no_change_workload({"app": content}))


if __name__ == "__main__":
    unittest.main()
