"""Messages from a disabled cloud account must be dropped, not ingested.

Regression cover for the prod stall on 2026-09-01: a cloud account that had been
status='disabled' since 2024 still had a live agent publishing to the shared
`k8s_agent_events` queue. Every one of its events ran the full enrichment path,
which calls back to that same (unresponsive) agent over the relay, so each
message held a consumer slot until the 120s timeout. 99.2% of that account's
relay RPCs timed out, the shared queue grew to 12k messages / 194 MB, and four
other tenants' events were stuck behind it.

Runs offline: heavy deps are stubbed before importing the module, same as
test_event_close_scoping.py.
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

import handlers.account_status as account_status  # noqa: E402
import rabbitmq.rabbitmq_consumers as consumers  # noqa: E402

DISABLED_ACCOUNT = "c4a4f1d9-f6d9-4783-9a46-53dd02ea8117"
ACTIVE_ACCOUNT = "0b30143a-c0c4-43eb-9fdf-e5472f71d405"
TENANT = "0d511f65-e0c4-430d-a919-4cc77460c1bd"

_STATUSES = {DISABLED_ACCOUNT: "disabled", ACTIVE_ACCOUNT: "active"}


def _message(account_id):
    return {
        "tenant": TENANT,
        "cloud_account_id": account_id,
        "type": "Pod",
        "data": [],
        "finding": {"id": "f-1", "title": "t"},
    }


def _select_data(table_name, columns=None, conditions=None, cursor_factory=None):
    assert table_name == "cloud_accounts"
    status = _STATUSES.get((conditions or {}).get("id"))
    return [(status,)] if status else []


class TestDisabledAccountGate(unittest.TestCase):
    def setUp(self):
        account_status.reset_cache()
        self._db = mock.patch.object(account_status.database, "select_data", side_effect=_select_data)
        self._db.start()
        self.addCleanup(self._db.stop)

    def _run_callbacks(self, account_id):
        """Run all three consumer callbacks; return the handlers that were reached."""
        with mock.patch.object(consumers, "run_event_handler_async") as event, mock.patch.object(
            consumers, "run_async_discovery_handler"
        ) as discovery, mock.patch.object(consumers, "process_spend") as spend:
            consumers._event_callback(_message(account_id))
            consumers._discovery_callback(_message(account_id))
            consumers._spend_callback(_message(account_id))
            return {
                "event": event.called,
                "discovery": discovery.called,
                "spend": spend.called,
            }

    def test_disabled_account_reaches_no_handler(self):
        self.assertEqual(
            self._run_callbacks(DISABLED_ACCOUNT),
            {"event": False, "discovery": False, "spend": False},
        )

    def test_active_account_is_ingested(self):
        self.assertEqual(
            self._run_callbacks(ACTIVE_ACCOUNT),
            {"event": True, "discovery": True, "spend": True},
        )

    def test_unknown_account_fails_open(self):
        self.assertEqual(
            self._run_callbacks("00000000-0000-0000-0000-000000000000"),
            {"event": True, "discovery": True, "spend": True},
        )

    def test_db_error_fails_open(self):
        with mock.patch.object(account_status.database, "select_data", side_effect=RuntimeError("db down")):
            self.assertEqual(
                self._run_callbacks(DISABLED_ACCOUNT),
                {"event": True, "discovery": True, "spend": True},
            )

    def test_status_is_cached_per_account(self):
        with mock.patch.object(account_status.database, "select_data", side_effect=_select_data) as select_data:
            self._run_callbacks(DISABLED_ACCOUNT)
            self._run_callbacks(DISABLED_ACCOUNT)
            self.assertEqual(select_data.call_count, 1)

    def test_flag_off_ingests_disabled_account(self):
        with mock.patch.object(consumers.Configs, "DROP_DISABLED_ACCOUNT_MESSAGES", False):
            self.assertEqual(
                self._run_callbacks(DISABLED_ACCOUNT),
                {"event": True, "discovery": True, "spend": True},
            )

    def test_json_string_payload_is_gated(self):
        with mock.patch.object(consumers, "run_event_handler_async") as event:
            consumers._event_callback(json.dumps(_message(DISABLED_ACCOUNT)))
            self.assertFalse(event.called)

    def test_string_payload_reaches_the_handler_unchanged(self):
        """The gate parses a str payload only to read cloud_account_id off it.

        It deliberately does not hand the parsed dict onward: both handler entry
        points take `str | dict` and parse for themselves
        (run_event_handler_async, run_async_discovery_handler), and kombu is
        configured with accept=["application/json", ...] so a real message is
        already a dict by the time the callback sees it. Forwarding the original
        object keeps the gate out of the payload-validation business, which each
        handler already does with its own error messages.
        """
        payload = json.dumps(_message(ACTIVE_ACCOUNT))

        with mock.patch.object(consumers, "run_event_handler_async") as event:
            consumers._event_callback(payload)
            event.assert_called_once_with(payload)

        with mock.patch.object(consumers, "run_async_discovery_handler") as discovery:
            consumers._discovery_callback(payload)
            discovery.assert_called_once_with(payload)


if __name__ == "__main__":
    unittest.main()
