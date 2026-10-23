"""A namespace the API server is deleting must not be reported as live.

The agent sends every namespace it can see with `deleted: false` hardcoded, so
`deleted` alone never retires one. A namespace stuck in Terminating (a wedged
finalizer holds them for months) kept arriving that way and stayed
`is_active = true` forever -- the cluster page listed namespaces Kubernetes had
already deleted, and the Knowledge Graph was blamed for "missing" them when it
was the only surface that had it right (#41508).

`status.phase` was in the payload all along; discovery just dropped it.

Runs offline: heavy deps (redis / psycopg2 / clickhouse) are stubbed in
sys.modules before importing the handler, so no DB, Redis, or cluster is needed.
"""

import os
import sys
import unittest
from unittest import mock

# ---------------------------------------------------------------------------
# Environment + heavy-dependency stubs — MUST run before importing app code.
# ---------------------------------------------------------------------------
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

from handlers import discovery_handler as dh  # noqa: E402


def _ns(name, phase=None, deleted=False):
    """One namespace entry as the agent's convertNamespace emits it."""
    entry = {
        "name": name,
        "workload_count": 0,
        "pod_count": 0,
        "creation_time": "2024-01-01T00:00:00Z",
        "updated_at": 1_700_000_000_000,
        "deleted": deleted,
        "resource_version": "100",
    }
    if phase is not None:
        entry["phase"] = phase
    return entry


class TestNamespaceTerminating(unittest.TestCase):
    def _run(self, data):
        """Run namespace discovery with the DB mocked; return {name: is_active}
        from the rows it would upsert, plus the reconcile's live-resource names."""
        rows = {}
        staged = {}

        def fake_insert(table, data, **kwargs):
            for row in data:
                rows[row["name"]] = row["is_active"]

        def fake_stage(_account, _tenant, _kind, resources, *_a, **_k):
            for res_type, items in resources:
                staged[res_type] = [i["resourse_id"] for i in items]
            return False, None

        with mock.patch.object(dh.database, "insert_data", side_effect=fake_insert), mock.patch.object(
            dh.database, "run_query", return_value=[]
        ), mock.patch.object(dh, "stage_batch_and_should_reconcile", side_effect=fake_stage):
            dh.run_namespace_discovery(data, "tenant-1", "account-1")

        return rows, staged

    def test_terminating_namespace_is_not_active(self):
        rows, staged = self._run([_ns("prod", phase="Active"), _ns("cf", phase="Terminating")])

        self.assertTrue(rows["prod"], "an Active namespace must stay live")
        self.assertFalse(
            rows["cf"],
            "a Terminating namespace is already deleted in Kubernetes and must not be reported as live",
        )

        # It must also drop out of the reconcile's live set, so the sweep agrees
        # with the upsert instead of being contradicted by it.
        self.assertEqual(staged["Namespace"], ["prod"])

    def test_phase_absent_defaults_to_active(self):
        """An agent too old to send status.phase must behave exactly as before."""
        rows, staged = self._run([_ns("legacy")])

        self.assertTrue(rows["legacy"])
        self.assertEqual(staged["Namespace"], ["legacy"])

    def test_phase_present_but_null_is_treated_as_active(self):
        """A JSON null arrives as a present key holding None, which a .get()
        default would not catch. It must fall back to Active and read live."""
        entry = _ns("nullphase")
        entry["phase"] = None
        rows, staged = self._run([entry])

        self.assertTrue(rows["nullphase"])
        self.assertEqual(staged["Namespace"], ["nullphase"])

    def test_deleted_flag_still_retires(self):
        """The pre-existing `deleted` path is unchanged."""
        rows, staged = self._run([_ns("gone", phase="Active", deleted=True)])

        self.assertFalse(rows["gone"])
        self.assertEqual(staged["Namespace"], [])

    def test_deleted_and_terminating_together(self):
        rows, staged = self._run([_ns("both", phase="Terminating", deleted=True)])

        self.assertFalse(rows["both"])
        self.assertEqual(staged["Namespace"], [])


if __name__ == "__main__":
    unittest.main()
