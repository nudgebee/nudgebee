"""RabbitConsumer lifecycle contract tests.

These pin the three defects a 7-day Loki sweep surfaced in this consumer:

1. `cannot schedule new futures after shutdown` (~28k lines / 7d, every worker
   pod). stop() tore the executor down while the drain loop was still pulling.
   A first attempt (6e84324369) caught the RuntimeError and re-queued each
   message, which turned the race into a reject/redeliver spin lasting the whole
   termination grace period. Both shapes are asserted against here.
2. `PRECONDITION_FAILED - unknown delivery tag`. AMQP delivery tags are scoped to
   a channel; a worker thread that outlived a reconnect acked a stale tag, which
   made the broker close the new channel and reconnect again.
3. `'NoneType' object has no attribute 'drain_events'` killing the consumer
   thread for good, leaving a live pod consuming nothing.

Runs offline: heavy deps are stubbed in sys.modules before importing app code,
and both the broker Connection and topology declaration are mocked.
"""

import os
import sys
import threading
import time
import unittest
from unittest import mock

# ---------------------------------------------------------------------------
# Environment + heavy-dependency stubs — MUST run before importing app code.
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

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

from rabbitmq import rabbitmq_client as rc  # noqa: E402

_queue_seq = 0


def _unwrap_atexit_hook(hook):
    """Recover the callable a threading._register_atexit entry wraps.

    The wrapper differs by version — functools.partial on 3.9-3.12, a lambda
    closure on 3.13 — so unwrap both rather than matching on __name__.
    """
    func = getattr(hook, "func", None)  # functools.partial
    if func is not None:
        return func
    for cell in getattr(hook, "__closure__", None) or ():
        if callable(cell.cell_contents):
            return cell.cell_contents
    return hook


def _make_consumer(**kwargs) -> rc.RabbitConsumer:
    """A RabbitConsumer with the broker mocked out.

    Queue names are unique per consumer because the Prometheus helpers key their
    gauges on the queue name.
    """
    global _queue_seq
    _queue_seq += 1
    with mock.patch.object(rc, "Connection"), mock.patch.object(rc.RabbitConsumer, "_declare_topology"):
        return rc.RabbitConsumer(
            exchange_name="test-ex",
            queue_name=f"test-queue-{_queue_seq}",
            routing_key="test-rk",
            callback=kwargs.pop("callback", lambda _body: None),
            retry_delay=kwargs.pop("retry_delay", 0.01),
            **kwargs,
        )


class TestShutdownOrdering(unittest.TestCase):
    def test_stop_waits_for_drain_loop_before_shutting_executor_down(self):
        """The executor must outlive the drain loop, not the other way round.

        If shutdown() lands first, every message still being delivered hits a
        dead executor — the `cannot schedule new futures after shutdown` storm.
        """
        consumer = _make_consumer()
        order = []

        consumer._executor = mock.MagicMock()
        consumer._executor.shutdown.side_effect = lambda **_kw: order.append("executor_shutdown")

        def fake_drain(_self):
            # Stand in for ConsumerMixin.run(): poll should_stop, then return.
            while not consumer._stop_requested():
                time.sleep(0.005)
            order.append("drain_loop_exited")

        with mock.patch.object(rc.ConsumerMixin, "run", fake_drain):
            thread = threading.Thread(target=consumer.run)
            thread.start()
            time.sleep(0.05)
            consumer.stop(timeout=5.0)
            thread.join(timeout=5.0)

        self.assertFalse(thread.is_alive(), "consumer thread did not exit")
        self.assertEqual(order, ["drain_loop_exited", "executor_shutdown"])

    def test_dead_executor_stops_consumer_instead_of_requeueing_each_message(self):
        """A dead executor must stop consumption, not bounce messages back.

        reject(requeue=True) here sends the message straight back to this same
        dead consumer, which is what produced the unbounded spin.
        """
        consumer = _make_consumer()
        consumer._executor = mock.MagicMock()
        consumer._executor.submit.side_effect = RuntimeError("cannot schedule new futures after shutdown")

        message = mock.MagicMock()
        message.body = b"{}"

        consumer._on_message({}, message)

        message.reject.assert_not_called()
        self.assertTrue(consumer._stop_requested(), "consumer kept consuming with a dead executor")
        consumer.connection.close.assert_called()

    def test_exit_hook_is_registered_ahead_of_the_thread_pool_teardown(self):
        """threading._shutdown runs its hooks in reverse-registration order.

        concurrent.futures registers its own when this module imports
        ThreadPoolExecutor, so ours must be registered after that import to run
        first. Without the hook nothing stops the consumers at all under
        gunicorn, and the consumer threads must then be daemons.
        """
        if rc._register_thread_atexit is None:
            self.skipTest("threading._register_atexit unavailable")

        from concurrent.futures import thread as futures_thread

        hooks = [_unwrap_atexit_hook(h) for h in threading._threading_atexits]
        self.assertIn(rc._stop_consumers_at_exit, hooks, "consumer shutdown hook was never registered")
        self.assertIn(futures_thread._python_exit, hooks)
        self.assertGreater(
            hooks.index(rc._stop_consumers_at_exit),
            hooks.index(futures_thread._python_exit),
            "consumer shutdown hook must be registered after concurrent.futures' so it runs first",
        )


class TestDeliveryTagEpoch(unittest.TestCase):
    def test_stale_epoch_skips_ack(self):
        consumer = _make_consumer()
        message = mock.MagicMock()

        epoch = consumer._connection_epoch
        consumer._bump_epoch()  # a reconnect happened while the worker was busy

        self.assertFalse(consumer._settle(message, epoch))
        message.ack.assert_not_called()
        message.reject.assert_not_called()

    def test_stale_epoch_skips_reject(self):
        consumer = _make_consumer()
        message = mock.MagicMock()

        epoch = consumer._connection_epoch
        consumer._bump_epoch()

        self.assertFalse(consumer._settle(message, epoch, reject=True))
        message.reject.assert_not_called()

    def test_current_epoch_acks(self):
        consumer = _make_consumer()
        message = mock.MagicMock()

        self.assertTrue(consumer._settle(message, consumer._connection_epoch))
        message.ack.assert_called_once()

    def test_reconnect_during_processing_leaves_the_message_unsettled(self):
        """End-to-end: a handler that outlives its channel must not ack."""
        consumer = _make_consumer(callback=lambda _body: consumer._bump_epoch())
        message = mock.MagicMock()

        consumer._process_message_in_thread({}, message, consumer._connection_epoch)

        message.ack.assert_not_called()
        message.reject.assert_not_called()

    def test_force_reconnect_invalidates_outstanding_tags(self):
        consumer = _make_consumer()
        epoch = consumer._connection_epoch

        consumer._force_reconnect()

        self.assertNotEqual(epoch, consumer._connection_epoch)


class TestConsumerSelfHeal(unittest.TestCase):
    def test_run_restarts_after_an_unhandled_error(self):
        """A crash in the drain loop must not kill the thread permanently."""
        consumer = _make_consumer()
        attempts = []

        def flaky_drain(_self):
            attempts.append(1)
            if len(attempts) == 1:
                raise AttributeError("'NoneType' object has no attribute 'drain_events'")
            consumer.should_stop = True

        with mock.patch.object(rc.ConsumerMixin, "run", flaky_drain), mock.patch.object(
            rc.RabbitConsumer, "_new_connection"
        ):
            consumer.run()

        self.assertEqual(len(attempts), 2, "consumer did not restart after the error")
        self.assertTrue(consumer._run_finished.is_set())

    def test_restart_backoff_is_interrupted_by_stop(self):
        """A pod terminating mid-backoff must not wait the delay out.

        An uninterruptible sleep here holds shutdown for up to
        MAX_CONSUMER_RESTART_BACKOFF and earns a SIGKILL.
        """
        consumer = _make_consumer(retry_delay=30.0)

        def always_fails(_self):
            raise OSError("broker down")

        with mock.patch.object(rc.ConsumerMixin, "run", always_fails), mock.patch.object(
            rc.RabbitConsumer, "_new_connection"
        ):
            thread = threading.Thread(target=consumer.run)
            thread.start()
            time.sleep(0.05)
            started = time.monotonic()
            consumer.stop(timeout=5.0)
            thread.join(timeout=5.0)
            elapsed = time.monotonic() - started

        self.assertFalse(thread.is_alive())
        self.assertLess(elapsed, 5.0, "stop() waited out the restart backoff instead of interrupting it")

    def test_run_does_not_restart_once_stop_was_requested(self):
        consumer = _make_consumer()
        attempts = []

        def failing_drain(_self):
            attempts.append(1)
            consumer.should_stop = True
            raise OSError("broker went away during shutdown")

        with mock.patch.object(rc.ConsumerMixin, "run", failing_drain):
            consumer.run()

        self.assertEqual(len(attempts), 1)


if __name__ == "__main__":
    unittest.main()
