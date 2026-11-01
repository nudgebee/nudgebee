"""An agent integration row is shared by every cluster in the tenant.

The collector keeps one `integrations` row per (tenant, provider) for the agent's own
logs / metrics / traces backends, and links each reporting account to it. That row's
status used to be written from whichever cluster's heartbeat landed last, so a cluster
with the signal off disabled the provider for every other cluster in the tenant until
one of them reported again.

Nothing about one cluster's health is written to the shared row any more:

- the account's link says the cluster has the provider configured, up or down;
- the row's status is the user's switch, kept in step with the marker api-server writes
  when a user disables the integration;
- whether the signal is up stays in the agent's own connection_status.

An agent leaves a provider's name out of its post when the signal is not configured, so
a cluster that stops naming a provider (or names a different one) is unlinked from the
one it named last time.

Runs offline: heavy deps are stubbed before importing the handler, same as
test_telemetry_health_checked_at.py. Whether the statements do what they say against
a real database is not something this harness can show.
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

import handlers.telemetry_handler as th  # noqa: E402

# Synthetic, and deliberately patterned so nobody mistakes one for a real row.
TENANT = "11111111-1111-4111-8111-111111111111"
ACCOUNT = "22222222-2222-4222-8222-222222222222"
AGENT = "33333333-3333-4333-8333-333333333333"
INTEGRATION = "44444444-4444-4444-8444-444444444444"

PROMETHEUS_URL = "http://prometheus.monitoring.svc:9090"
# What an agent with all three signals on reported last time.
ALL_ON = {
    "logsConnection": True,
    "logsConnectionProvider": "loki",
    "prometheusConnection": True,
    "prometheusUrl": PROMETHEUS_URL,
    "tracesEnabled": True,
    "traceProvider": "otel_clickhouse",
}


class TestAgentIntegrationRow(unittest.TestCase):
    def _upsert(self, connection_enabled):
        """Run _upsert_integration for the metrics signal; return the statements it ran
        (with their params) and the default-provider claim mock.

        sql.SQL is swapped for the identity so statements reach run_query as text.
        """
        statements = []

        def run_query(statement, params=None):
            statements.append((" ".join(statement.split()), params))
            return [[INTEGRATION]] if statement.lstrip().startswith("INSERT INTO integrations (") else []

        with mock.patch.object(th.sql, "SQL", side_effect=lambda text: text), mock.patch.object(
            th.database, "run_query", side_effect=run_query
        ), mock.patch.object(th, "_maybe_set_default_provider") as claim_default:
            th._upsert_integration(
                TENANT,
                ACCOUNT,
                "prometheus",
                connection_enabled,
                "metricsConnectionProvider",
                default_column="default_metrics_provider",
            )
        return statements, claim_default

    def test_a_signal_that_is_off_still_links_the_account(self):
        """The link says the cluster has the provider, not that it is healthy: deleting
        it on every dip would drop the per-cluster settings stored on it."""
        statements, claim_default = self._upsert(connection_enabled=False)

        upsert, link = statements
        self.assertTrue(upsert[0].startswith("INSERT INTO integrations ("))
        self.assertTrue(link[0].startswith("INSERT INTO integrations_cloud_accounts"))
        self.assertEqual(link[1], [INTEGRATION, ACCOUNT, TENANT])
        claim_default.assert_not_called()

    def test_health_is_never_written_to_the_shared_row(self):
        """The same statement, with the same values, whether the signal is up or down."""
        up, _ = self._upsert(connection_enabled=True)
        down, _ = self._upsert(connection_enabled=False)

        self.assertEqual(up[0][0], down[0][0])
        self.assertEqual(up[0][1][5], "enabled")
        self.assertEqual(down[0][1][5], "enabled")

    def test_row_status_follows_the_user_disabled_marker(self):
        """On an existing row the status is derived from the marker api-server writes on
        a manual Disable -- which is what lets that Disable survive heartbeats, and what
        re-enables a row an older collector disabled from a cluster's health."""
        statements, _ = self._upsert(connection_enabled=True)
        upsert, params = statements[0]

        self.assertIn(
            "SET status = CASE WHEN EXISTS ( SELECT 1 FROM integration_config_values m "
            "WHERE m.integration_id = integrations.id AND m.name = %s ) THEN 'disabled' ELSE 'enabled' END",
            upsert,
        )
        self.assertEqual(params[-1], th.USER_DISABLED_CONFIG_NAME)
        self.assertEqual(th.USER_DISABLED_CONFIG_NAME, "user_disabled")

    def test_default_provider_is_claimed_only_while_connected(self):
        _, claim_default = self._upsert(connection_enabled=True)
        claim_default.assert_called_once_with(INTEGRATION, ACCOUNT, TENANT, "default_metrics_provider")


class TestProviderNoLongerNamed(unittest.TestCase):
    def _post(self, activity_stats, previous):
        """Handle one telemetry post from an agent whose stored connection_status is
        `previous`; return the (provider, provider_type) links removed and the
        (provider, connection_enabled) pairs upserted."""
        released, upserted = [], []

        def run_query(_statement, params=None):
            # The one statement whose result is read: the lookup of what this agent
            # reported last time, identified by its single param.
            return [[previous]] if params == [AGENT] else []

        with mock.patch.object(th.database, "run_query", side_effect=run_query), mock.patch.object(
            th, "_notify_scanner_heartbeat"
        ), mock.patch.object(
            th, "_upsert_integration", side_effect=lambda *args, **kwargs: upserted.append((args[2], args[3]))
        ), mock.patch.object(
            th,
            "_unlink_integration",
            side_effect=lambda tenant, account, provider, provider_type: released.append((provider, provider_type)),
        ):
            th.handle_telemetry(
                TENANT, ACCOUNT, AGENT, {"version": "0.1.21", "stats": {}, "activity_stats": activity_stats}
            )
        return released, upserted

    def test_a_signal_that_stops_naming_its_provider_is_unlinked_from_it(self):
        now = {k: v for k, v in ALL_ON.items() if k != "logsConnectionProvider"}
        now["logsConnection"] = False

        released, _ = self._post(now, previous=ALL_ON)

        self.assertEqual(released, [("loki", "logsConnectionProvider")])

    def test_a_removed_prometheus_url_unlinks_prometheus(self):
        now = {k: v for k, v in ALL_ON.items() if k != "prometheusUrl"}
        now["prometheusConnection"] = False

        released, _ = self._post(now, previous=ALL_ON)

        self.assertEqual(released, [("prometheus", "metricsConnectionProvider")])

    def test_a_switched_provider_is_unlinked_from_the_old_one_and_linked_to_the_new(self):
        released, upserted = self._post({**ALL_ON, "logsConnectionProvider": "ES"}, previous=ALL_ON)

        self.assertEqual(released, [("loki", "logsConnectionProvider")])
        self.assertIn(("ES", True), upserted)

    def test_unchanged_providers_release_nothing(self):
        released, _ = self._post(dict(ALL_ON), previous=ALL_ON)
        self.assertEqual(released, [])

    def test_a_provider_now_named_by_another_signal_is_kept(self):
        """Chronosphere serves metrics and traces; dropping it from one signal while the
        other still names it must not unlink the account."""
        previous = {**ALL_ON, "prometheusUrl": "https://metrics.chronosphere.example.com"}
        now = {k: v for k, v in ALL_ON.items() if k != "prometheusUrl"}
        now.update({"prometheusConnection": False, "traceProvider": "chronosphere"})

        released, _ = self._post(now, previous=previous)

        self.assertNotIn(("chronosphere", "metricsConnectionProvider"), released)

    def test_a_post_without_stats_releases_nothing(self):
        """An empty or absent activity_stats says nothing about any signal, and neither
        does one that carries none of the signals' flags."""
        for label, activity_stats in (("empty dict", {}), ("null", None), ("unrelated keys only", {"foo": 1})):
            with self.subTest(activity_stats=label):
                released, _ = self._post(activity_stats, previous=ALL_ON)
                self.assertEqual(released, [])

    def test_only_the_signals_a_post_reports_on_are_acted_on(self):
        """A post carrying one signal's flag must not unlink the providers of the others."""
        released, _ = self._post({"logsConnection": False}, previous=ALL_ON)
        self.assertEqual(released, [("loki", "logsConnectionProvider")])

    def test_every_named_provider_is_upserted_whether_its_signal_is_up_or_down(self):
        _, upserted = self._post({**ALL_ON, "prometheusConnection": False, "tracesEnabled": False}, previous=ALL_ON)

        self.assertEqual(upserted, [("loki", True), ("prometheus", False), ("otel_clickhouse", False)])

    def test_a_malformed_stored_status_does_not_stop_the_post(self):
        """The stored names are read before the agent row is updated; a value that is not
        a string must count as "not named", or one bad row would fail every later post."""
        released, upserted = self._post(dict(ALL_ON), previous={"prometheusUrl": 123, "logsConnectionProvider": ["x"]})

        self.assertEqual(released, [])
        self.assertIn(("prometheus", True), upserted)

    def test_a_first_post_has_nothing_to_release(self):
        released, _ = self._post(dict(ALL_ON), previous=None)
        self.assertEqual(released, [])


if __name__ == "__main__":
    unittest.main()
