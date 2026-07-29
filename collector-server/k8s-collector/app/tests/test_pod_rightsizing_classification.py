"""Classification contract for pod_right_sizing categories.

A workload where NO container has any cpu/memory request set is a
best-practice finding and lands under Configuration; any set request keeps
the row under RightSizing. The predicate must run on the MERGED workload
payload — never per container — and must stay in sync with its twin in
ml-k8s-server (same table below) and the backfill migration's SQL predicate.

Runs offline: heavy deps are stubbed before importing the handler, same as
test_discovery_shape.py.
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

from handlers.event_handler import classify_pod_right_sizing_category  # noqa: E402


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


class TestClassifyPodRightSizingCategory(unittest.TestCase):
    def test_cases(self):
        for name, payload, expected in CASES:
            with self.subTest(name):
                self.assertEqual(classify_pod_right_sizing_category(payload), expected)


if __name__ == "__main__":
    unittest.main()
