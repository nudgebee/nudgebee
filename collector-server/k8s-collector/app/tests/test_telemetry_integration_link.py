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


def holder(source="agent", status="enabled", is_self=False, marked=False):
    """One link of the account that holds the default column, as the claim reads it."""
    return (source, status, is_self, marked)


class TestDefaultClaimDecision(unittest.TestCase):
    """Who keeps an account's default when the agent's connected provider heartbeats.

    A default the user set in the UI carries a mark on its link; the agent never takes a
    marked or user-sourced default, but still takes one only an agent claim put there.
    """

    def test_nothing_holds_the_default_so_the_agent_claims_it(self):
        self.assertEqual(th._default_claim_decision([]), "claim")

    def test_the_agent_already_holds_it(self):
        self.assertEqual(th._default_claim_decision([holder(is_self=True)]), "keep")

    def test_a_user_sourced_default_is_left_alone(self):
        self.assertEqual(th._default_claim_decision([holder(source="user")]), "keep")

    def test_an_agent_row_the_user_picked_is_left_alone(self):
        """The bug: a user picks the agent's ClickHouse row for logs, and Loki's next
        heartbeat took it back because the row's source is 'agent'."""
        self.assertEqual(th._default_claim_decision([holder(marked=True)]), "keep")

    def test_an_agent_row_only_an_agent_claim_put_there_is_taken_over(self):
        """Unmarked agent-held defaults heal as before, e.g. one left on a provider the
        cluster switched away from."""
        self.assertEqual(th._default_claim_decision([holder()]), "claim")

    def test_a_disabled_pick_does_not_block_the_agent(self):
        self.assertEqual(th._default_claim_decision([holder(marked=True, status="disabled")]), "claim")
        self.assertEqual(th._default_claim_decision([holder(source="user", status="disabled")]), "claim")

    def test_the_agent_gives_way_when_an_enabled_pick_also_holds_it(self):
        """Two rows hold the default: a pick re-enabled after a Disable, or a heartbeat
        that raced a UI save. The agent clears its own claim."""
        self.assertEqual(th._default_claim_decision([holder(is_self=True), holder(marked=True)]), "release")
        self.assertEqual(th._default_claim_decision([holder(is_self=True), holder(source="user")]), "release")

    def test_the_agent_never_clears_a_pick_of_its_own_row(self):
        self.assertEqual(
            th._default_claim_decision([holder(is_self=True, marked=True), holder(source="user")]),
            "keep",
        )

    def test_a_default_the_user_turned_off_is_claimed_back(self):
        """Decided with the k8s-agent#660 maintainer: turning off an agent-held default
        without picking another leaves nothing holding it, so the next heartbeat claims it
        again. With no default the resolver routes to the agent's own provider anyway, so
        "off" could not change what serves the signal; the toggle shows what does."""
        self.assertEqual(th._default_claim_decision([]), "claim")


class _FakeSQL(str):
    """psycopg2.sql stand-in: identifiers render as their names, so statements are text."""

    def format(self, *args, **kwargs):
        return _FakeSQL(str.format(self, *args, **{k: str(v) for k, v in kwargs.items()}))


class TestDefaultClaimStatements(unittest.TestCase):
    def _claim(self, holders):
        statements = []

        def run_query(statement, params=None):
            statements.append((" ".join(str(statement).split()), params))
            return list(holders) if str(statement).lstrip().startswith("SELECT") else []

        fake_sql = mock.Mock(SQL=_FakeSQL, Identifier=lambda name: name)
        with mock.patch.object(th, "sql", fake_sql), mock.patch.object(th.database, "run_query", side_effect=run_query):
            th._maybe_set_default_provider(INTEGRATION, ACCOUNT, TENANT, "default_log_provider")
        return [s for s, _ in statements[1:]], statements

    def test_keep_writes_nothing(self):
        writes, _ = self._claim([holder(marked=True)])
        self.assertEqual(writes, [])

    def test_claim_resets_only_unmarked_agent_rows_and_guards_against_any_enabled_pick(self):
        writes, statements = self._claim([holder()])

        self.assertEqual(len(writes), 2)
        reset, set_self = writes
        self.assertIn("SET default_log_provider = false", reset)
        self.assertIn("i.source = 'agent'", reset)
        self.assertIn("NOT (%s = ANY(ica.user_set_defaults))", reset)
        self.assertIn("SET default_log_provider = true", set_self)
        self.assertIn("ui.status != 'disabled'", set_self)
        self.assertIn("ui.source = 'user' OR %s = ANY(u.user_set_defaults)", set_self)
        self.assertIn("default_log_provider", statements[1][1])

    def test_release_clears_only_the_agents_own_unmarked_link(self):
        writes, statements = self._claim([holder(is_self=True), holder(marked=True)])

        self.assertEqual(len(writes), 1)
        self.assertIn("SET default_log_provider = false", writes[0])
        self.assertIn("ica.integration_id = %s", writes[0])
        self.assertIn("NOT (%s = ANY(ica.user_set_defaults))", writes[0])
        self.assertEqual(statements[1][1][0], INTEGRATION)


class _PGError(Exception):
    def __init__(self, pgcode):
        super().__init__(pgcode)
        self.pgcode = pgcode


class TestDefaultClaimBeforeMigration(unittest.TestCase):
    """The collector can roll out before the migration adding user_set_defaults applies,
    wherever it deploys independently of the migrations Job. Until then it leaves the
    default alone instead of failing the rest of the heartbeat."""

    def _claim_with(self, error):
        run_query = mock.Mock(side_effect=error)
        fake_sql = mock.Mock(SQL=_FakeSQL, Identifier=lambda name: name)
        with mock.patch.object(th, "sql", fake_sql), mock.patch.object(th.database, "run_query", run_query):
            th._maybe_set_default_provider(INTEGRATION, ACCOUNT, TENANT, "default_log_provider")
        return run_query

    def test_a_missing_column_skips_the_default_and_writes_nothing(self):
        run_query = self._claim_with(_PGError("42703"))
        self.assertEqual(run_query.call_count, 1)

    def test_any_other_database_error_still_raises(self):
        with self.assertRaises(_PGError):
            self._claim_with(_PGError("40P01"))


# The agent keeps naming its Loki for logs and reports its ClickHouse logs table beside
# it (nudgebee/k8s-agent#660): clickhouseLogsTable is the table checked, empty when the
# agent has no ClickHouse; clickhouseLogsConnection says the table is present.
CLICKHOUSE_LOGS = {**ALL_ON, "clickhouseLogsConnection": True, "clickhouseLogsTable": "default.otel_logs"}


class TestClickHouseLogsTable(unittest.TestCase):
    def _post(self, activity_stats, previous):
        """Handle one telemetry post; return the (provider, provider_type) links removed
        and each upsert as (provider, connection_enabled, provider_type, default_column)."""
        released, upserted = [], []

        def run_query(_statement, params=None):
            return [[previous]] if params == [AGENT] else []

        with mock.patch.object(th.database, "run_query", side_effect=run_query), mock.patch.object(
            th, "_notify_scanner_heartbeat"
        ), mock.patch.object(
            th,
            "_upsert_integration",
            side_effect=lambda *args, **kwargs: upserted.append((args[2], args[3], args[4], kwargs["default_column"])),
        ), mock.patch.object(
            th,
            "_unlink_integration",
            side_effect=lambda tenant, account, provider, provider_type: released.append((provider, provider_type)),
        ):
            th.handle_telemetry(
                TENANT, ACCOUNT, AGENT, {"version": "0.1.28", "stats": {}, "activity_stats": activity_stats}
            )
        return released, upserted

    def test_a_reported_logs_table_links_clickhouse_as_a_logs_candidate_without_claiming(self):
        """So a user can pick ClickHouse for logs, while the agent's Loki keeps the claim."""
        _, upserted = self._post(dict(CLICKHOUSE_LOGS), previous=None)

        self.assertIn(("otel_clickhouse", True, "clickhouseLogsTable", None), upserted)
        self.assertIn(("loki", True, "logsConnectionProvider", "default_log_provider"), upserted)

    def test_moving_traces_away_keeps_the_link_logs_use(self):
        now = {k: v for k, v in CLICKHOUSE_LOGS.items() if k != "traceProvider"}
        now["tracesEnabled"] = False

        released, _ = self._post(now, previous=CLICKHOUSE_LOGS)

        self.assertEqual(released, [])

    def test_clickhouse_used_by_neither_logs_nor_traces_is_unlinked(self):
        now = {k: v for k, v in CLICKHOUSE_LOGS.items() if k != "traceProvider"}
        now.update({"tracesEnabled": False, "clickhouseLogsConnection": False, "clickhouseLogsTable": ""})

        released, _ = self._post(now, previous=CLICKHOUSE_LOGS)

        self.assertIn("otel_clickhouse", [provider for provider, _ in released])

    def test_an_agent_without_clickhouse_links_nothing_for_it(self):
        now = {**ALL_ON, "clickhouseLogsConnection": False, "clickhouseLogsTable": "", "traceProvider": "jaeger"}

        _, upserted = self._post(now, previous=None)

        self.assertNotIn("otel_clickhouse", [provider for provider, *_ in upserted])

    def test_an_older_agent_without_the_fields_keeps_the_link_traces_use(self):
        """A post without clickhouseLogsConnection says nothing about the table."""
        released, _ = self._post(dict(ALL_ON), previous=CLICKHOUSE_LOGS)

        self.assertEqual(released, [])


class TestClickHouseLogsCollectedFlag(unittest.TestCase):
    """clickhouseLogsCollected gates ClickHouse as a logs provider. The merge into
    connection_status keeps a key a post leaves out, so the agent UPDATE drops this one
    when a post that reports flags omits it: a `true` cannot outlive the agent that sent
    it (a rollback to an agent that predates the flag)."""

    def _agent_update(self, activity_stats):
        """Handle one telemetry post; return the agent UPDATE's text and params.

        sql.SQL is swapped for the identity so the statement reaches run_query as text."""
        updates = []

        def run_query(statement, params=None):
            if params and params[-1] == AGENT and len(params) == 7:
                updates.append((" ".join(statement.split()), params))
            return []

        with mock.patch.object(th.sql, "SQL", side_effect=lambda text: text), mock.patch.object(
            th.database, "run_query", side_effect=run_query
        ), mock.patch.object(th, "_notify_scanner_heartbeat"), mock.patch.object(th, "_upsert_integration"):
            th.handle_telemetry(
                TENANT, ACCOUNT, AGENT, {"version": "0.1.28", "stats": {}, "activity_stats": activity_stats}
            )
        self.assertEqual(len(updates), 1)
        return updates[0]

    def test_the_update_removes_the_keys_it_is_given_after_the_merge(self):
        statement, params = self._agent_update(dict(CLICKHOUSE_LOGS))

        self.assertIn(
            "connection_status = (COALESCE(connection_status, '{}'::jsonb) || %s::jsonb) - %s::text[]", statement
        )
        self.assertEqual(params[4], ["clickhouseLogsCollected"])

    def test_a_post_that_sends_the_flag_keeps_what_it_says(self):
        for value in (True, False):
            with self.subTest(clickhouseLogsCollected=value):
                _, params = self._agent_update({**CLICKHOUSE_LOGS, "clickhouseLogsCollected": value})

                self.assertEqual(params[4], [])
                self.assertIs(json.loads(params[3])["clickhouseLogsCollected"], value)

    def test_a_post_that_reports_no_flags_clears_nothing(self):
        """An empty or malformed activity_stats says nothing about any flag."""
        for activity_stats in ({}, None, "not json"):
            with self.subTest(activity_stats=activity_stats):
                _, params = self._agent_update(activity_stats)

                self.assertEqual(params[4], [])


if __name__ == "__main__":
    unittest.main()
