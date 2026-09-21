"""The collector's queues must actually be declared with their arguments.

Regression cover for a bug that was invisible for the life of this module:
`_declare_topology` built its queues with ``Queue(arguments=...)``, but kombu's
Queue keeps a fixed attribute list (``queue_arguments``, ``binding_arguments``,
``consumer_arguments``, ...) and drops unknown kwargs without raising. So every
collector queue was declared bare -- no x-message-ttl and no
x-dead-letter-exchange -- while the code read as though it had both. Confirmed
on live brokers: `k8s_agent_discovery` reported ``arguments: {}`` where
relay-server's queues on the same broker carried theirs.

Two consequences, both of which produced customer-visible failure:

* Nothing expired. The only way a message ever left `k8s_agent_discovery` was a
  successful consume, so a stalled consumer grew the broker's disk until the
  volume was full.
* Nothing dead-lettered. ``reject(requeue=False)`` on a queue with no
  dead-letter-exchange DISCARDS. Every "rejecting message" log line was silent
  data loss, and the .dlq queues were unreachable by construction.

A unit test cannot see the broker, so these assert the thing that was actually
wrong: the Queue object we hand to kombu carries what we think it carries, and
the declare path and the consume path agree on it.

Runs offline: heavy deps are stubbed before importing app code, same as
test_consumer_lifecycle.py.
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

from amqp.exceptions import PreconditionFailed  # noqa: E402
from kombu import Exchange  # noqa: E402

from rabbitmq import rabbitmq_client as rc  # noqa: E402

_queue_seq = 0

TTL_MS = 3600000


def _make_consumer(**kwargs) -> rc.RabbitConsumer:
    """A RabbitConsumer with the broker and the declare mocked out.

    Queue names are unique per consumer because the metrics helpers key their
    gauges on the queue name.
    """
    global _queue_seq
    _queue_seq += 1
    with mock.patch.object(rc, "Connection"), mock.patch.object(rc.RabbitConsumer, "_declare_topology"):
        return rc.RabbitConsumer(
            exchange_name="test-ex",
            queue_name=f"test-queue-{_queue_seq}",
            routing_key="test-rk",
            callback=lambda _body: None,
            message_ttl=kwargs.pop("message_ttl", TTL_MS),
            retry_delay=kwargs.pop("retry_delay", 0.01),
            **kwargs,
        )


def _exchange() -> Exchange:
    return Exchange("test-ex", type="direct", durable=True, auto_delete=False)


class TestQueueArgumentsReachKombu(unittest.TestCase):
    def test_main_queue_carries_ttl_and_dead_letter_arguments(self):
        """The regression. `arguments=` silently produced queue_arguments=None."""
        consumer = _make_consumer()

        queue = consumer._main_queue(_exchange())

        self.assertEqual(
            queue.queue_arguments,
            {
                "x-dead-letter-exchange": "test-ex_dlx",
                "x-dead-letter-routing-key": "test-rk",
                "x-message-ttl": TTL_MS,
            },
        )

    def test_ttl_is_omitted_when_not_configured(self):
        consumer = _make_consumer(message_ttl=None)

        queue = consumer._main_queue(_exchange())

        self.assertNotIn("x-message-ttl", queue.queue_arguments)
        self.assertIn("x-dead-letter-exchange", queue.queue_arguments)

    def test_declare_and_consume_agree_on_arguments(self):
        """Drift here is a 406 on the first reconnect, not a startup error.

        kombu's Consumer re-declares its queues every time the connection is
        revived, so a queue declared one way at startup and another way on
        reconnect leaves a live pod that can never reattach.
        """
        consumer = _make_consumer()
        captured = {}

        def fake_consumer(queues, **_kwargs):
            captured["queue"] = queues[0]
            return mock.MagicMock()

        consumer.get_consumers(fake_consumer, mock.MagicMock())

        self.assertEqual(
            captured["queue"].queue_arguments,
            consumer._main_queue(_exchange()).queue_arguments,
        )


class TestLegacyQueueFallback(unittest.TestCase):
    """A queue that predates the fix exists without our arguments.

    RabbitMQ will not change a live queue's arguments, so the declare answers
    PRECONDITION_FAILED forever. _declare_topology runs from __init__, which
    runs at gunicorn import time, so retrying that would mean the pod never
    boots -- an upgrade would take out every collector at once.
    """

    @staticmethod
    def _consumer_with_legacy_broker():
        consumer = _make_consumer()
        channel = mock.MagicMock()

        def queue_declare(*args, **kwargs):
            name = kwargs.get("queue") or (args[0] if args else "")
            if name == consumer.queue_name:  # the .dlq declares fine
                raise PreconditionFailed("inequivalent arg 'x-message-ttl'")
            return mock.MagicMock()

        channel.queue_declare.side_effect = queue_declare
        consumer.connection = mock.MagicMock()
        consumer.connection.channel.return_value = channel
        return consumer, channel

    def test_precondition_failed_attaches_instead_of_retrying(self):
        consumer, channel = self._consumer_with_legacy_broker()

        consumer._declare_topology()  # must return; a retry loop hangs the test

        self.assertTrue(consumer._legacy_queue)
        self.assertEqual(
            consumer.connection.channel.call_count,
            1,
            "PRECONDITION_FAILED must not be retried -- that is the boot wedge",
        )
        channel.close.assert_called_once()

    def test_legacy_queue_is_declared_bare_and_still_declared(self):
        """Bare, so it matches the broker. Still declared, so a wipe self-heals.

        kombu re-declares on every reconnect, and that is what puts the queue
        back when the broker comes up without it -- a recreated PVC being the
        case that matters. Marking the queue no_declare would look like the
        right way to say "attach to what exists" and would silently remove the
        only thing that recreates it.
        """
        consumer, _channel = self._consumer_with_legacy_broker()
        consumer._declare_topology()

        queue = consumer._main_queue(_exchange())

        self.assertIsNone(queue.queue_arguments, "must match the bare queue the broker holds")
        self.assertFalse(queue.no_declare, "must still be declared so a wiped broker gets it back")

    def test_deleting_the_legacy_queue_lets_a_running_consumer_pick_up_arguments(self):
        """The operator's remedy must work without restarting the pod.

        Deleting the bare queue is how an existing installation gets the TTL and
        the dead-letter-exchange. If the "this queue is legacy" decision were
        sticky for the process lifetime, the next reconnect would recreate the
        queue bare and silently undo exactly what the operator just did.
        """
        consumer, channel = self._consumer_with_legacy_broker()
        consumer._declare_topology()
        self.assertTrue(consumer._legacy_queue, "precondition: started out on a legacy broker")

        # The operator deletes the bare queue; the broker now takes our arguments.
        channel.queue_declare.side_effect = None
        channel.queue_declare.return_value = mock.MagicMock()

        captured = {}

        def fake_consumer(queues, **_kwargs):
            captured["queue"] = queues[0]
            return mock.MagicMock()

        consumer.get_consumers(fake_consumer, mock.MagicMock())

        self.assertFalse(consumer._legacy_queue)
        self.assertIn(
            "x-message-ttl",
            captured["queue"].queue_arguments,
            "reconnect after the delete must declare the queue we actually want",
        )

    def test_connection_errors_are_still_retried(self):
        """Only 406 is terminal. A broker that is merely down must be waited for."""
        consumer = _make_consumer()
        consumer.connection = mock.MagicMock()
        consumer.connection.channel.side_effect = [OSError("broker down"), mock.MagicMock()]

        consumer._declare_topology()

        self.assertFalse(consumer._legacy_queue)
        self.assertEqual(consumer.connection.channel.call_count, 2)


if __name__ == "__main__":
    unittest.main()
