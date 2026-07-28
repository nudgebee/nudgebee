"""Discovery → source-row shape contract test (the Python seam of the KG E2E suite).

The Go knowledge-graph E2E tests assume specific column shapes in the source
tables (k8s_workloads / k8s_pods / cloud_resourses). Those rows are written by
THIS service's discovery handler. This test feeds a sample discovery message
through run_async_discovery_handler with the DB mocked, captures the rows it
would upsert, and asserts they carry the exact fields the Go converters read
(sources/k8s_source.go fetchK8sWorkloads / pods, aws/gcp/azure cloud_resourses).

If discovery starts writing a different shape, this fails — signalling the Go
fixtures need updating, closing the cross-language drift gap.

Runs offline: heavy deps (redis / psycopg2 / clickhouse) are stubbed in
sys.modules before importing the handler, so no DB, Redis, or cluster is needed.
"""

import os
import sys
import unittest
from contextlib import contextmanager
from unittest import mock

# ---------------------------------------------------------------------------
# Environment + heavy-dependency stubs — MUST run before importing app code.
# config.Configs is a pydantic settings model validated at import time; the DB
# and Redis clients import drivers we don't want (or have) installed.
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

# Ensure the app package root is importable when run standalone.
_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

from handlers import discovery_handler as dh  # noqa: E402


def _service_message():
    """A minimal INCREMENTAL 'service' discovery message (no batch metadata, no
    full_load), so the reconcile/cleanup paths are skipped and we exercise just
    the upsert shape for a Workload and a Pod."""
    return {
        "type": "service",
        "tenant": "tenant-1",
        "cloud_account_id": "account-1",
        "data": [
            {
                "service_key": "prod/Deployment/api",
                "type": "Deployment",
                "deleted": False,
                "name": "api",
                "namespace": "prod",
                "creation_time": "2024-01-01T00:00:00Z",
                "update_time": 1_700_000_000_000,
                "status": "Running",
                "total_pods": 3,
                "ready_pods": 3,
                "resource_version": "100",
                "config": {"labels": {"app": "api"}, "owner": []},
            },
            {
                "service_key": "prod/Pod/api-abc",
                "type": "Pod",
                "deleted": False,
                "name": "api-abc",
                "namespace": "prod",
                "creation_time": "2024-01-01T00:00:00Z",
                "update_time": 1_700_000_000_000,
                "status": "Running",
                "node_name": "node-1",
                "restart_count": {"api": 0},
                "resource_version": "101",
                "config": {"labels": {"app": "api"}, "owner": [{"name": "api", "kind": "Deployment"}]},
            },
        ],
    }


class TestDiscoveryShape(unittest.TestCase):
    def _run_and_capture(self, message):
        """Run the discovery handler with DB/lock/kg mocked; return {table: [rows]}."""
        captured = {}

        def fake_insert(table, data, **kwargs):
            captured.setdefault(table, []).extend(data)

        @contextmanager
        def fake_lock(*_a, **_k):
            yield

        with mock.patch.object(dh.database, "insert_data", side_effect=fake_insert), mock.patch.object(
            dh.database, "run_query", return_value=[]
        ), mock.patch.object(dh, "acquire_discovery_lock", fake_lock), mock.patch.object(dh, "publish_kg_update"):
            dh.run_async_discovery_handler(message)

        return captured

    def test_service_discovery_row_shapes(self):
        captured = self._run_and_capture(_service_message())

        # All three source tables the Go converters read must be written.
        for table in ("cloud_resourses", "k8s_workloads", "k8s_pods"):
            self.assertIn(table, captured, f"discovery did not write to {table}")

        # cloud_resourses — the AWS/GCP/Azure/k8s converters read these.
        cr = captured["cloud_resourses"][0]
        for key in ("account", "resourse_id", "type", "service_name", "meta", "external_resource_id", "name", "tenant"):
            self.assertIn(key, cr, f"cloud_resourses row missing {key!r} (Go converter reads it)")
        self.assertEqual(cr["service_name"], "kubernetes")

        # k8s_workloads — sources/k8s_source.go fetchK8sWorkloads reads exactly these
        # (last_deployed_time is populated by a DB trigger, not the insert).
        wl = captured["k8s_workloads"][0]
        for key in (
            "cloud_resource_id",
            "tenant_id",
            "cloud_account_id",
            "kind",
            "namespace",
            "name",
            "is_active",
            "meta",
            "labels",
            "creation_time",
        ):
            self.assertIn(key, wl, f"k8s_workloads row missing {key!r} (Go converter reads it)")
        self.assertEqual(wl["kind"], "Deployment")
        self.assertEqual(wl["namespace"], "prod")

        # k8s_pods — read by the k8s converter's pod path + read-time pod synthesis.
        pod = captured["k8s_pods"][0]
        for key in (
            "cloud_resource_id",
            "tenant_id",
            "cloud_account_id",
            "namespace",
            "name",
            "status",
            "node_name",
            "is_active",
            "workload_type",
            "workload_name",
            "labels",
            "meta",
        ):
            self.assertIn(key, pod, f"k8s_pods row missing {key!r} (Go converter reads it)")
        self.assertEqual(pod["workload_name"], "api")
        self.assertEqual(pod["workload_type"], "Deployment")


if __name__ == "__main__":
    unittest.main()
