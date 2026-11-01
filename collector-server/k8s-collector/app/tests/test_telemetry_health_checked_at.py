"""The agent's health flags must carry the time they were reported.

Agent Details shows a per-signal status for logs / metrics / traces. A signal served
by an integration is probed by a cron that stamps its own `providerStatus.checkedAt`,
so that row can say how old its verdict is. A signal served by the agent had no such
stamp: the flags arrive in `activity_stats` and the payload carries no timestamp, so
a cluster whose telemetry stopped hours ago still rendered "Connected" with nothing on
screen to betray the age -- observed on a dev cluster whose posts were 401ing while its
relay stayed healthy, which keeps every other freshness signal on the page looking fine.

`last_connected_at` cannot stand in: relay-server also writes it on connect and
disconnect, so a reconnect refreshes it without a single flag being re-reported.

Runs offline: heavy deps are stubbed before importing the handler, same as
test_job_last_seen.py.
"""

import datetime
import json
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

import handlers.telemetry_handler as th  # noqa: E402

# Synthetic, and deliberately patterned so nobody mistakes one for a real row.
TENANT = "11111111-1111-4111-8111-111111111111"
ACCOUNT = "22222222-2222-4222-8222-222222222222"
AGENT = "33333333-3333-4333-8333-333333333333"
INTEGRATION = "44444444-4444-4444-8444-444444444444"

AGENT_FLAGS = {
    "logsConnection": True,
    "logsConnectionProvider": "loki",
    "prometheusConnection": True,
    "prometheusUrl": "http://prometheus.monitoring.svc:9090",
    "tracesEnabled": True,
    "traceProvider": "otel_clickhouse",
    "nodeAgentConnection": True,
}


def _telemetry(activity_stats=None, include_key=True):
    payload = {"version": "0.1.21", "stats": {"k8s_version": "v1.35.7", "provider": "GKE"}}
    if include_key:
        payload["activity_stats"] = activity_stats
    return payload


def _recorder(calls):
    """Record every run_query call, and answer only the one query whose result is read.

    _upsert_integration takes result[0][0] from its RETURNING id (10 params); every other
    statement here either ignores its result or, like _maybe_set_default_provider's
    two-column SELECT, must see no rows. psycopg2 is a MagicMock in this harness, so
    sql.SQL(...) is a mock and the params are the only thing that identifies a statement.
    """

    def record(_statement, params=None):
        calls.append(params)
        return [[INTEGRATION]] if params and len(params) == 10 else []

    return record


class TestTelemetryHealthCheckedAt(unittest.TestCase):
    def _handle(self, payload):
        """Run the handler offline and return the params of the agent UPDATE.

        psycopg2 is a MagicMock in this harness, so sql.SQL(...) is a mock and the
        statement text is not assertable -- the agent UPDATE is identified by its
        param shape instead of its position, because _upsert_cloud_account_attrs and
        the integration upserts fire straight after it with their own run_query calls.
        """
        calls = []

        # _notify_scanner_heartbeat posts to api-server on a daemon thread.
        with mock.patch.object(th.database, "run_query", side_effect=_recorder(calls)), mock.patch.object(
            th, "_notify_scanner_heartbeat"
        ):
            th.handle_telemetry(TENANT, ACCOUNT, AGENT, payload)

        agent_updates = [p for p in calls if p and p[-1] == AGENT and len(p) == 6]
        self.assertEqual(len(agent_updates), 1, f"expected exactly one agent UPDATE, got {len(agent_updates)}")
        return agent_updates[0]

    def _connection_status(self, params):
        return json.loads(params[3])

    def test_flags_are_stamped_with_the_time_they_were_reported(self):
        params = self._handle(_telemetry(dict(AGENT_FLAGS)))
        merged = self._connection_status(params)
        self.assertIn(
            "healthCheckedAt",
            merged,
            "agent-reported flags reached connection_status with no report time; "
            "Agent Details cannot then say how old they are",
        )
        # Same instant as last_connected_at, not merely close to it.
        last_connected_at = params[1]
        self.assertIsInstance(last_connected_at, datetime.datetime)
        self.assertEqual(merged["healthCheckedAt"], last_connected_at.strftime("%Y-%m-%dT%H:%M:%SZ"))

    def test_stamp_is_tz_aware_utc(self):
        """A naive local timestamp would be silently right in a UTC container and hours
        wrong on a laptop -- the UI appends Z to any offset-less string it parses."""
        params = self._handle(_telemetry(dict(AGENT_FLAGS)))
        self.assertEqual(params[1].tzinfo, datetime.timezone.utc)
        self.assertTrue(self._connection_status(params)["healthCheckedAt"].endswith("Z"))

    def test_the_agents_own_flags_survive_the_stamp(self):
        merged = self._connection_status(self._handle(_telemetry(dict(AGENT_FLAGS))))
        for flag, value in AGENT_FLAGS.items():
            self.assertEqual(merged[flag], value)

    def test_a_json_string_payload_is_stamped_too(self):
        """The agent may send activity_stats as an encoded string."""
        merged = self._connection_status(self._handle(_telemetry(json.dumps(AGENT_FLAGS))))
        self.assertIn("healthCheckedAt", merged)
        self.assertTrue(merged["logsConnection"])

    def test_no_flags_reported_means_no_stamp(self):
        """An empty, absent or malformed activity_stats refreshes no flag, so dating one
        would claim a freshness the row does not have."""
        for label, payload in (
            ("empty dict", _telemetry({})),
            ("key absent", _telemetry(include_key=False)),
            ("null", _telemetry(None)),
            ("undecodable string", _telemetry("not json")),
        ):
            with self.subTest(activity_stats=label):
                self.assertNotIn("healthCheckedAt", self._connection_status(self._handle(payload)))

    def test_integration_upsert_does_not_see_the_server_owned_key(self):
        """The stamp is ours, not the agent's; it must not leak into the integration rows
        derived from the same payload."""
        seen = []

        def spy(tenant_id, cloud_account_id, provider, connection_enabled, provider_type, default_column=None):
            seen.append(provider)

        calls = []

        with mock.patch.object(th.database, "run_query", side_effect=_recorder(calls)), mock.patch.object(
            th, "_notify_scanner_heartbeat"
        ), mock.patch.object(th, "_upsert_integration", side_effect=spy):
            th.handle_telemetry(TENANT, ACCOUNT, AGENT, _telemetry(dict(AGENT_FLAGS)))

        self.assertNotIn("healthCheckedAt", seen)
        self.assertIn("loki", seen)


if __name__ == "__main__":
    unittest.main()
