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

from amqp.exceptions import NotFound, PreconditionFailed  # noqa: E402
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

    def test_consumer_queue_is_never_redeclared_by_kombu(self):
        """kombu re-declares a Consumer's queues on every reconnect.

        With our arguments, that is a 406 PRECONDITION_FAILED on every queue
        created before the fix -- a burst of broker channel errors on every pod
        start. _declare_topology owns creating the queue; the consumer only
        attaches.
        """
        consumer = _make_consumer()
        captured = {}

        def fake_consumer(queues, **_kwargs):
            captured["queue"] = queues[0]
            return mock.MagicMock()

        consumer.get_consumers(fake_consumer, mock.MagicMock())

        self.assertTrue(captured["queue"].no_declare)


class TestMainQueueCreatedOnlyWhenMissing(unittest.TestCase):
    """Existing queues are used as they are; only a missing one is created.

    Brokers hold these queues in two shapes: without arguments (created before
    the fix -- every long-lived install) and with them (created since). RabbitMQ
    answers a declare whose arguments differ from the live queue's with 406 and
    never changes a live queue, so the existence check must not send arguments.
    """

    @staticmethod
    def _consumer(main_queue_exists: bool):
        consumer = _make_consumer()
        first, second = mock.MagicMock(name="first"), mock.MagicMock(name="second")

        def queue_declare(*args, **kwargs):
            name = kwargs.get("queue") or (args[0] if args else "")
            if name == consumer.queue_name and kwargs.get("passive") and not main_queue_exists:
                raise NotFound("no queue")
            return mock.MagicMock()

        first.queue_declare.side_effect = queue_declare
        # kombu passes the arguments through the channel's transport hook.
        second.prepare_queue_arguments.side_effect = lambda arguments, **_kw: arguments
        consumer.connection = mock.MagicMock()
        consumer.connection.clone.return_value.channel.side_effect = [first, second]
        return consumer, first, second

    @staticmethod
    def _main_declares(channel, name):
        return [c for c in channel.queue_declare.call_args_list if c.kwargs.get("queue") == name]

    def test_existing_queue_is_checked_passively_and_bound(self):
        consumer, first, _second = self._consumer(main_queue_exists=True)

        consumer._declare_topology()

        declares = self._main_declares(first, consumer.queue_name)
        self.assertEqual(len(declares), 1)
        self.assertTrue(declares[0].kwargs["passive"])
        self.assertNotIn("arguments", declares[0].kwargs, "an existence check must not compare arguments")
        self.assertIn(
            mock.call(queue=consumer.queue_name, exchange="test-ex", routing_key="test-rk"),
            first.queue_bind.call_args_list,
        )
        self.assertEqual(consumer.connection.clone.return_value.channel.call_count, 1)
        consumer.connection.clone.return_value.release.assert_called_once()

    def test_missing_queue_is_created_with_arguments(self):
        """This is what recreates the queue after a broker comes up empty."""
        consumer, _first, second = self._consumer(main_queue_exists=False)

        consumer._declare_topology()

        declares = self._main_declares(second, consumer.queue_name)
        self.assertEqual(len(declares), 1)
        self.assertFalse(declares[0].kwargs["passive"])
        self.assertEqual(declares[0].kwargs["arguments"], consumer._queue_arguments())

    def test_precondition_failed_is_not_retried(self):
        """Retrying cannot win -- RabbitMQ never changes a live entity."""
        consumer = _make_consumer()
        channel = mock.MagicMock()
        channel.queue_declare.side_effect = PreconditionFailed("inequivalent arg")
        consumer.connection = mock.MagicMock()
        consumer.connection.clone.return_value.channel.return_value = channel

        consumer._declare_topology()  # must return; a retry loop hangs the test

        self.assertEqual(consumer.connection.clone.return_value.channel.call_count, 1)

    def test_connection_errors_are_still_retried(self):
        """A broker that is merely down must be waited for."""
        consumer = _make_consumer()
        consumer.connection = mock.MagicMock()
        consumer.connection.clone.return_value.channel.side_effect = [OSError("broker down"), mock.MagicMock()]

        consumer._declare_topology()

        self.assertEqual(consumer.connection.clone.return_value.channel.call_count, 2)


if __name__ == "__main__":
    unittest.main()
