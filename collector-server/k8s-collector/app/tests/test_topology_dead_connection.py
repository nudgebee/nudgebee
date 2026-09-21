"""_declare_topology must not retry a Connection that can never revive.

Incident, dev, 2026-09-19: RabbitMQ (on a spot node) was preempted while the
discovery consumer had messages in flight. A worker thread's ack failed, so
_force_reconnect() close()d the shared Connection. kombu then called
get_consumers() to revive, which calls _declare_topology() -- and kombu's
Connection.connection property is gated on self._closed, so a closed Connection
returns None for good. Every .channel() raised "'NoneType' object has no
attribute 'channel'", the `while True` retried the same dead object 250 times
over 21 minutes, get_consumers() never returned, and the queue sat at 0
consumers with 3,876 messages backed up until the pod was restarted by hand.

Events and spend recovered normally on the same broker in the same process:
their queues were empty, so no worker thread ever called _force_reconnect().
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

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

from rabbitmq import rabbitmq_client as rc  # noqa: E402

_queue_seq = 0


class _ClosedConnection:
    """A kombu Connection that has been close()d: .channel() can never work."""

    def __init__(self):
        self._closed = True
        self.released = False

    def channel(self):
        # Mirrors the real failure: Connection.connection returns None once
        # _closed is set, so attribute access on it blows up.
        raise AttributeError("'NoneType' object has no attribute 'channel'")

    def release(self):
        self.released = True


class _LiveConnection:
    """A usable Connection whose channel() succeeds."""

    def __init__(self):
        self._closed = False
        self.released = False

    def channel(self):
        return mock.MagicMock()

    def release(self):
        self.released = True


def _make_consumer(**kwargs) -> rc.RabbitConsumer:
    global _queue_seq
    _queue_seq += 1
    with mock.patch.object(rc, "Connection"), mock.patch.object(rc.RabbitConsumer, "_declare_topology"):
        return rc.RabbitConsumer(
            exchange_name="test-ex",
            queue_name=f"topo-queue-{_queue_seq}",
            routing_key="test-rk",
            callback=kwargs.pop("callback", lambda _body: None),
            retry_delay=kwargs.pop("retry_delay", 0.001),
            **kwargs,
        )


class TestTopologyDeadConnection(unittest.TestCase):
    def test_closed_connection_is_replaced_instead_of_retried_forever(self):
        """The declare loop must escape a closed Connection, not spin on it.

        Without the fix this test hangs: the loop retries the same closed
        object, which raises identically every time.
        """
        consumer = _make_consumer()
        dead = _ClosedConnection()
        live = _LiveConnection()
        consumer.connection = dead

        with mock.patch.object(rc.RabbitConsumer, "_new_connection", staticmethod(lambda: live)), mock.patch.object(
            rc, "Exchange"
        ), mock.patch.object(rc, "Queue"):
            consumer._declare_topology()

        self.assertIs(consumer.connection, live, "declare loop kept the closed connection")
        self.assertTrue(dead.released, "the closed connection was leaked instead of released")

    def test_live_connection_is_not_churned_on_an_unrelated_failure(self):
        """A declare failure that isn't the socket must not drop a live connection.

        The consumer's own channel rides on this Connection; replacing it for a
        transient declare error would tear down a working consumer.
        """
        consumer = _make_consumer()
        live = _LiveConnection()
        consumer.connection = live
        calls = {"n": 0}

        def flaky_exchange(*_a, **_kw):
            calls["n"] += 1
            if calls["n"] == 1:
                raise RuntimeError("broker still starting up")
            return mock.MagicMock()

        with mock.patch.object(rc.RabbitConsumer, "_new_connection", staticmethod(_LiveConnection)), mock.patch.object(
            rc, "Exchange", side_effect=flaky_exchange
        ), mock.patch.object(rc, "Queue"):
            consumer._declare_topology()

        self.assertIs(consumer.connection, live, "a live connection was replaced on a non-socket failure")
        self.assertFalse(live.released)

    def test_replacing_a_connection_invalidates_outstanding_delivery_tags(self):
        """The epoch must advance when the connection object is swapped.

        Delivery tags are scoped to a channel. A worker thread that finishes
        after the swap and acks its old tag against the new channel gets
        PRECONDITION_FAILED, which makes the broker kill that channel too --
        the failure the epoch mechanism exists to prevent.
        """
        consumer = _make_consumer()
        consumer.connection = _ClosedConnection()
        before = consumer._connection_epoch

        with mock.patch.object(rc.RabbitConsumer, "_new_connection", staticmethod(_LiveConnection)):
            consumer._replace_dead_connection()

        self.assertGreater(consumer._connection_epoch, before, "stale delivery tags were left valid")

    def test_noop_replacement_does_not_advance_the_epoch(self):
        """A live connection keeps its tags valid — bumping would reject good acks."""
        consumer = _make_consumer()
        consumer.connection = _LiveConnection()
        before = consumer._connection_epoch

        with mock.patch.object(rc.RabbitConsumer, "_new_connection", staticmethod(_LiveConnection)):
            consumer._replace_dead_connection()

        self.assertEqual(consumer._connection_epoch, before)

    def test_replace_dead_connection_is_a_noop_on_a_live_connection(self):
        consumer = _make_consumer()
        live = _LiveConnection()
        consumer.connection = live

        with mock.patch.object(rc.RabbitConsumer, "_new_connection", staticmethod(_LiveConnection)):
            consumer._replace_dead_connection()

        self.assertIs(consumer.connection, live)

    def test_replace_dead_connection_handles_a_none_connection(self):
        consumer = _make_consumer()
        consumer.connection = None
        live = _LiveConnection()

        with mock.patch.object(rc.RabbitConsumer, "_new_connection", staticmethod(lambda: live)):
            consumer._replace_dead_connection()

        self.assertIs(consumer.connection, live)


if __name__ == "__main__":
    unittest.main()
