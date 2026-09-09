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
import handlers.upgrade_handler as uh  # noqa: E402

# The archive predicate is only half the invariant. The upsert that follows it
# must not reclaim status from EXCLUDED, or a row the archive just tombstoned
# comes straight back as Open. Both halves are asserted below, because fixing
# either one alone leaves the finding reopening.
STATUS_GUARD = "status = CASE WHEN recommendation.status NOT IN ('Open', 'Archive')"


class TestRecommendationArchivePredicate(unittest.TestCase):
    def _captured_sql(self, fn, *args, module=eh):
        with mock.patch.object(module.database, "run_query") as run_query:
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

    # upgrade_handler keeps its own copies of these helpers, which is how they
    # were missed when the event_handler twins were fixed. The EKS and k8s
    # version writers all route through them.

    def test_upgrade_multi_rule_archive_tombstones_open_only(self):
        sql = self._captured_sql(
            uh.archive_existing_recommendations_multi_rule,
            "acct",
            "tenant",
            "InfraUpgrade",
            ["a", "b"],
            module=uh,
        )
        self.assertIn("status = 'Open'", sql)
        self.assertNotIn("not in ('Archive')", sql)

    def test_upgrade_with_rule_archive_tombstones_open_only(self):
        sql = self._captured_sql(
            uh.archive_existing_with_rule,
            "acct",
            "tenant",
            "InfraUpgrade",
            "eks_cluster_upgrade",
            module=uh,
        )
        # This helper previously had no status filter at all, so it archived
        # every row for the rule regardless of who owned its state.
        self.assertIn("status = 'Open'", sql)


class TestRecommendationUpsertGuard(unittest.TestCase):
    """The upsert must never move a row out of a user-owned state."""

    def test_shared_upgrade_conflict_clause_is_guarded(self):
        self.assertIn(STATUS_GUARD, uh.ON_CONFLICT_RECOMMENDATION)
        self.assertNotIn("status=EXCLUDED.status", uh.ON_CONFLICT_RECOMMENDATION)

    def test_event_handler_conflict_clauses_are_guarded(self):
        # These are built inline rather than shared, so assert over the source:
        # every ON CONFLICT on the recommendation table has to carry the guard.
        with open(eh.__file__) as handle:
            source = handle.read()
        self.assertNotIn(
            "status=EXCLUDED.status",
            source,
            "an inline ON CONFLICT still reclaims status, which reopens dismissed findings",
        )
        self.assertGreaterEqual(
            source.count(STATUS_GUARD),
            5,
            "fewer guarded upserts than expected; a producer may have been added without the guard",
        )


if __name__ == "__main__":
    unittest.main()
