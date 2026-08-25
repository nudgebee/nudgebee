"""A resolved alert must publish the resolved-notification post-process message
exactly when the status UPDATE actually flips a row, and stay silent when the
row was already closed (repeated resolve alerts, consumer redelivery).

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

import handlers.event_handler as eh  # noqa: E402


class TestResolvedEventNotify(unittest.TestCase):
    def _resolve(self, update_result):
        event = {"id": "evt-1", "status": "FIRING", "tenant": "t-1"}
        with mock.patch.object(eh.database, "run_query", return_value=update_result), mock.patch.object(
            eh.rabbitmq_client, "publish_message"
        ) as publish, mock.patch.object(eh, "_write_events_to_clickhouse"):
            eh.handle_existing_resolved_event("acct-1", event)
        return publish

    def test_transition_publishes_notify_resolved(self):
        publish = self._resolve(update_result=[("evt-1",)])
        self.assertTrue(publish.called)
        args = publish.call_args[0]
        self.assertEqual(args[0], "event_post_process_exchange")
        self.assertEqual(args[1], "event_post_process")
        self.assertEqual(args[2], {"event_id": "evt-1", "notify_resolved": True})

    def test_already_closed_stays_silent(self):
        publish = self._resolve(update_result=[])
        self.assertFalse(publish.called)


if __name__ == "__main__":
    unittest.main()
