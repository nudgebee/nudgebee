"""A Job's last_seen must reflect when the agent saw it, not 1970.

The agent names this value `updated_at` for Jobs and CronJobs but `update_time`
for Deployments/StatefulSets/DaemonSets. The collector read only `update_time`,
so every Job row fell through to `or 0` and landed at 1970-01-01 -- observed on
dev as all 82 Jobs and all 4 CronJobs, while every other kind was current. That
silently makes Jobs look 56 years stale to anything keying on freshness.

Runs offline: heavy deps are stubbed before importing the handler, same as
test_resolved_event_notify.py.
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

for _mod in (
    "redis",
    "psycopg2",
    "psycopg2.extras",
    "psycopg2.pool",
    "clickhouse_driver",
    "clickhouse_connect",
    "clickhouse_connect.driver",
    "clickhouse_connect.driver.client",
    "clickhouse_connect.driver.query",
    "clickhouse_connect.driver.summary",
):
    sys.modules.setdefault(_mod, mock.MagicMock())

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

import handlers.discovery_handler as dh  # noqa: E402

TENANT = "890cad87-c452-4aa7-b84a-742cee0454a1"
ACCOUNT = "a2a30b02-0f67-42e5-a2ab-c658230fd798"
# 2026-08-19T15:00:00Z
SEEN_AT_MS = 1787151600000


def _job(**overrides):
    payload = {
        "name": "nightly-backup",
        "namespace": "ops",
        "type": "Job",
        "service_key": "ops/Job/nightly-backup",
        "created_at": "2026-08-19T14:00:00Z",
        "updated_at": SEEN_AT_MS,
        "deleted": False,
        "completions": 1,
        "job_data": {},
        "status": {},
        "config": {"labels": {}},
        "resource_version": 1,
    }
    payload.update(overrides)
    return payload


class TestJobLastSeen(unittest.TestCase):
    def _discover(self, payload):
        captured = {}

        def capture(table, rows, **kwargs):
            captured.setdefault(table, []).extend(rows)

        with mock.patch.object(dh.database, "insert_data", side_effect=capture), mock.patch.object(
            dh.database, "run_query", return_value=[]
        ), mock.patch.object(dh, "stage_batch_and_should_reconcile", return_value=(False, None)), mock.patch.object(
            dh, "publish_kg_update"
        ):
            dh.run_job_discovery([payload], TENANT, ACCOUNT, is_last_batch=True, is_first_batch=True)
        return captured

    def test_job_last_seen_comes_from_the_agent_timestamp(self):
        captured = self._discover(_job())
        workloads = captured.get("k8s_workloads", [])
        self.assertTrue(workloads, "expected the job to be written to k8s_workloads")
        last_seen = workloads[0]["last_seen"]
        self.assertTrue(
            str(last_seen).startswith("2026-08-19"),
            f"last_seen = {last_seen}; expected the agent's observation time, not the epoch",
        )

    def test_update_time_is_still_accepted(self):
        """Deployments and friends use this name; a future agent may unify on it."""
        payload = _job()
        del payload["updated_at"]
        payload["update_time"] = SEEN_AT_MS
        workloads = self._discover(payload).get("k8s_workloads", [])
        self.assertTrue(str(workloads[0]["last_seen"]).startswith("2026-08-19"))

    def test_missing_timestamp_still_falls_back_to_the_epoch(self):
        """No timestamp at all keeps the existing sentinel rather than inventing now()."""
        payload = _job()
        del payload["updated_at"]
        workloads = self._discover(payload).get("k8s_workloads", [])
        self.assertTrue(str(workloads[0]["last_seen"]).startswith("1970-01-01"))


if __name__ == "__main__":
    unittest.main()
