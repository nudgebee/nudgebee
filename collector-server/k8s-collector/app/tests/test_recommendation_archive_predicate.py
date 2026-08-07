"""Archive step must tombstone Open rows only.

A user-set state (Dismissed/snoozed, InProgress, Closed) has to survive the next
scan so the upsert's CASE guard can preserve it. When the archive matched every
non-Archive row it flipped a Dismissed row to Archive first, and the upsert then
reopened the finding as Open with is_dismissed stranded at true — a row no read
path expects (nothing filters on is_dismissed).

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

import handlers.event_handler as eh  # noqa: E402


class TestRecommendationArchivePredicate(unittest.TestCase):
    def _captured_sql(self, fn, *args):
        with mock.patch.object(eh.database, "run_query") as run_query:
            fn(*args)
        self.assertTrue(run_query.called, "archive helper must issue a query")
        return run_query.call_args[0][0]

    def test_image_scan_archive_tombstones_open_only(self):
        sql = self._captured_sql(eh.archive_image_scan_recommendations, "acct", "tenant", "img:1.0")
        self.assertIn("status = 'Open'", sql)
        self.assertNotIn("status != 'Archive'", sql)

    def test_with_rule_archive_tombstones_open_only(self):
        sql = self._captured_sql(eh.archive_existing_with_rule, "acct", "tenant", "Security", "image_scan")
        self.assertIn("status = 'Open'", sql)

    def test_with_rules_archive_tombstones_open_only(self):
        sql = self._captured_sql(eh.archive_existing_with_rules, "acct", "tenant", "Configuration", ["a", "b"])
        self.assertIn("status = 'Open'", sql)

    def test_pod_right_sizing_archive_tombstones_open_only(self):
        sql = self._captured_sql(eh.archive_existing_recommendation, "acct", "tenant")
        self.assertIn("status = 'Open'", sql)
        self.assertNotIn("not in", sql)


if __name__ == "__main__":
    unittest.main()
