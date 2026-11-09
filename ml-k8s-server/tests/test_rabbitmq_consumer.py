"""The queue consumer takes one message at a time, acks it when the scan ends, and never stops.

The reconnect tests drive `run()` itself. It loops forever by design and survives any exception,
so each test ends it from the patched reconnect sleep, which raises `_Stop` on its n-th call.
"""

import asyncio
import json
import logging
from types import SimpleNamespace

import pytest
from kombu.transport.pyamqp import Channel as KombuChannel

import server.utils.rabbitmq as rabbitmq
from server.exception import CancelPrediction
from server.message import MlMessageAbstract, ml_server_message_handler


class _Stop(BaseException):
    pass


class FakeChannel:
    def __init__(self, events: list):
        self.events = events
        self.on_message = None

    def exchange_declare(self, exchange, type, durable, auto_delete):
        self.events.append(("exchange_declare", exchange))

    def queue_declare(self, queue, durable, exclusive, auto_delete):
        self.events.append(("queue_declare", queue))

    def queue_bind(self, queue, exchange, routing_key):
        self.events.append(("queue_bind", queue, exchange, routing_key))

    def basic_qos(self, prefetch_size, prefetch_count, a_global):
        self.events.append(("basic_qos", prefetch_size, prefetch_count, a_global))

    def basic_consume(self, queue, callback, no_ack):
        self.on_message = callback
        self.events.append(("basic_consume", queue, no_ack))

    def basic_ack(self, delivery_tag):
        self.events.append(("ack", delivery_tag))

    def prepare_message(self, *args, **kwargs):
        return KombuChannel.prepare_message(self, *args, **kwargs)

    def basic_publish(self, message, exchange, routing_key):
        self.events.append(("publish", exchange, routing_key, message))


class FakeBroker:
    """Stands in for kombu's Connection; `drains[i]` scripts what the i-th connection's drain does.

    Like kombu, reading `.connection` again on a connection hands back a fresh bare one with no
    consumer on it, which is how the consumer used to end up draining a connection that never
    delivers anything.
    """

    def __init__(self, drains: list, connect_failures: int = 0):
        self.drains = drains
        self.connect_failures = connect_failures
        self.connections: list = []
        self.events: list = []

    def __call__(self, url):
        broker = self

        class FakeConnection:
            def __init__(self):
                self.closed = False
                self.reads = 0
                self.channel = FakeChannel(broker.events)
                drain = broker.drains[len(broker.connections)]
                self.raw = SimpleNamespace(channel=lambda: self.channel, drain_events=lambda: drain(self))
                broker.connections.append(self)

            @property
            def connection(self):
                self.reads += 1
                if self.reads > 1:
                    return SimpleNamespace(drain_events=broker.drain_bare)
                return self.raw

            def connect(self):
                if broker.connect_failures:
                    broker.connect_failures -= 1
                    raise ConnectionRefusedError("broker unreachable")

            def close(self):
                self.closed = True

        return FakeConnection()

    def drain_bare(self):
        # A real bare connection blocks forever; raising instead keeps a regression from hanging the test.
        self.events.append(("drain", "bare"))
        raise ConnectionResetError()


class Sleeps(list):
    """Records each reconnect delay; the n-th one raises _Stop."""

    def __init__(self, limit: int):
        super().__init__()
        self.limit = limit

    def __call__(self, delay):
        self.append(delay)
        if len(self) == self.limit:
            raise _Stop()


def _run(monkeypatch, broker: FakeBroker, sleeps_until_stop: int, callback=None) -> Sleeps:
    sleeps = Sleeps(sleeps_until_stop)
    monkeypatch.setattr(rabbitmq.time, "sleep", sleeps)
    monkeypatch.setattr(rabbitmq, "Connection", broker)
    consumer = rabbitmq.KombuConsumerThread("amqp://broker//", "jobs", "jobs", "jobs", callback or (lambda body: None))
    with pytest.raises(_Stop):
        consumer.run()
    return sleeps


def _reset(connection):
    raise ConnectionResetError(104, "reset by peer")


def _consumer(handled: list) -> rabbitmq.KombuConsumerThread:
    return rabbitmq.KombuConsumerThread("amqp://broker//", "jobs", "jobs", "jobs", handled.append)


def _message(
    channel: FakeChannel, body: str, redelivered: bool = False, tag: int = 1, headers: dict | None = None
) -> SimpleNamespace:
    properties = {
        "content_type": "application/json",
        "content_encoding": "utf-8",
        "delivery_mode": 2,
        "application_headers": headers,
    }
    return SimpleNamespace(
        body=body,
        headers=headers,
        properties=properties,
        delivery_tag=tag,
        delivery_info={"redelivered": redelivered},
        channel=channel,
    )


SCAN = json.dumps({"action_name": "vertical_rightsizing_request", "account_id": "acc-1", "datadog_api_key": "k"})


def test_message_is_acked_only_after_the_handler_returns():
    events: list = []
    channel = FakeChannel(events)
    consumer = rabbitmq.KombuConsumerThread("amqp://broker//", "jobs", "jobs", "jobs", lambda body: events.append(body))

    consumer.on_message(_message(channel, "scan", tag=5))

    assert events == ["scan", ("ack", 5)]


def test_redelivered_message_is_requeued_once_instead_of_run_or_dropped():
    handled: list = []
    channel = FakeChannel([])

    _consumer(handled).on_message(
        _message(channel, SCAN, redelivered=True, tag=9, headers={"traceparent": "00-ab-cd-01"})
    )

    assert handled == []
    (publish, exchange, routing_key, retry), ack = channel.events
    assert (publish, exchange, routing_key) == ("publish", "jobs", "jobs")
    assert ack == ("ack", 9)
    assert retry.body == SCAN
    assert retry.properties["application_headers"] == {"traceparent": "00-ab-cd-01", rabbitmq.ATTEMPT_HEADER: 2}
    assert retry.properties["content_type"] == "application/json"
    assert retry.properties["delivery_mode"] == 2


def test_a_retry_that_comes_back_redelivered_is_dropped():
    handled: list = []
    channel = FakeChannel([])

    _consumer(handled).on_message(
        _message(channel, SCAN, redelivered=True, tag=9, headers={rabbitmq.ATTEMPT_HEADER: 2})
    )

    assert handled == []
    assert channel.events == [("ack", 9)]


def test_a_retry_delivered_normally_runs():
    handled: list = []
    channel = FakeChannel([])

    _consumer(handled).on_message(_message(channel, SCAN, tag=9, headers={rabbitmq.ATTEMPT_HEADER: 2}))

    assert handled == [SCAN]
    assert channel.events == [("ack", 9)]


@pytest.mark.parametrize("headers", [None, {rabbitmq.ATTEMPT_HEADER: 2}])
def test_redelivered_message_is_logged_without_its_body(caplog, headers):
    _consumer([]).on_message(_message(FakeChannel([]), SCAN, redelivered=True, headers=headers))

    assert "vertical_rightsizing_request for account acc-1" in caplog.text
    assert "datadog_api_key" not in caplog.text


def test_failed_message_goes_to_the_dead_letter_exchange_and_is_acked(monkeypatch):
    published: list = []
    monkeypatch.setattr(rabbitmq, "publish_message", lambda **kwargs: published.append(kwargs))
    channel = FakeChannel([])

    def fail(body):
        raise RuntimeError("scan failed")

    consumer = rabbitmq.KombuConsumerThread("amqp://broker//", "jobs", "jobs", "jobs", fail)
    consumer.on_message(_message(channel, "scan", tag=3))

    assert published == [
        {"exchange_name": "jobs_dlx", "routing_key": "jobs", "message": {"payload": "scan", "error": "scan failed"}}
    ]
    assert channel.events == [("ack", 3)]


class FakeConnection:
    def __init__(self, url):
        pass

    def __enter__(self):
        return self

    def __exit__(self, *exc_info):
        return False

    def channel(self):
        return None


class FakeProducer:
    def __init__(self, channel):
        pass

    def publish(self, message, **kwargs):
        pass


def test_publishing_logs_where_a_message_goes_and_not_what_it_carries(monkeypatch, caplog):
    monkeypatch.setattr(rabbitmq, "Connection", FakeConnection)
    monkeypatch.setattr(rabbitmq, "Producer", FakeProducer)
    caplog.set_level(logging.INFO)

    rabbitmq.publish_message("jobs", "jobs", json.loads(SCAN))

    assert "to queue jobs exchange jobs" in caplog.text
    assert "datadog_api_key" not in caplog.text


def test_failed_message_is_logged_by_name_without_its_body(monkeypatch, caplog):
    monkeypatch.setattr(rabbitmq, "Connection", FakeConnection)
    monkeypatch.setattr(rabbitmq, "Producer", FakeProducer)
    caplog.set_level(logging.INFO)

    def fail(body):
        raise RuntimeError("scan failed")

    consumer = rabbitmq.KombuConsumerThread("amqp://broker//", "jobs", "jobs", "jobs", fail)
    consumer.on_message(_message(FakeChannel([]), SCAN))

    assert "Unable to process vertical_rightsizing_request for account acc-1" in caplog.text
    assert "to queue jobs exchange jobs_dlx" in caplog.text
    assert "datadog_api_key" not in caplog.text


@pytest.mark.parametrize(
    "body",
    [
        {"action_name": "not_a_real_action", "datadog_api_key": "k"},
        {"datadog_api_key": "k"},
        {"action_name": "vertical_rightsizing_request", "datadog_api_key": "k"},
    ],
)
def test_rejected_message_error_does_not_repeat_its_body(body):
    with pytest.raises(ValueError) as rejected:
        ml_server_message_handler(json.dumps(body))

    assert "datadog_api_key" not in str(rejected.value)


@pytest.mark.parametrize(
    "body, described",
    [
        ({"action_name": "scan", "account_id": "acc-1"}, "scan for account acc-1"),
        ({"action_name": "scan", "account": "acc-1"}, "scan for account acc-1"),
        ({"action_name": "ml_recomm_sync", "recommendation_id": "r-1"}, "ml_recomm_sync"),
        ({"account_id": "acc-1"}, "an unnamed message for account acc-1"),
        ({}, "an unnamed message"),
    ],
)
def test_a_message_is_described_by_the_fields_it_has(body, described):
    assert rabbitmq._describe(json.dumps(body)) == described


def test_a_body_too_deep_to_parse_is_still_dead_lettered(monkeypatch):
    published: list = []
    monkeypatch.setattr(rabbitmq, "publish_message", lambda **kwargs: published.append(kwargs))

    def fail(body):
        raise RuntimeError("scan failed")

    consumer = rabbitmq.KombuConsumerThread("amqp://broker//", "jobs", "jobs", "jobs", fail)
    consumer.on_message(_message(FakeChannel([]), "[" * 100000))

    assert [call["exchange_name"] for call in published] == ["jobs_dlx"]


def test_cancelled_message_is_logged_without_its_fields(caplog):
    class CancelledScan(MlMessageAbstract):
        action_name: str = "cancelled_scan"
        span_name: str = "cancelled_scan"
        exchange: str = "jobs"
        queue: str = "jobs"
        datadog_api_key: str = "k"

        def process_message(self):
            raise CancelPrediction("no data")

    CancelledScan().process()

    assert "cancelled_scan" in caplog.text
    assert "datadog_api_key" not in caplog.text


def test_message_is_acked_even_when_the_dead_letter_publish_fails(monkeypatch):
    def broker_down(**kwargs):
        raise RuntimeError("dead-letter publish failed")

    def fail(body):
        raise RuntimeError("scan failed")

    monkeypatch.setattr(rabbitmq, "publish_message", broker_down)
    channel = FakeChannel([])
    consumer = rabbitmq.KombuConsumerThread("amqp://broker//", "jobs", "jobs", "jobs", fail)

    with pytest.raises(RuntimeError):
        consumer.on_message(_message(channel, "scan", tag=3))

    assert channel.events == [("ack", 3)]


def test_consumer_takes_one_message_at_a_time_and_acks_it_itself(monkeypatch):
    broker = FakeBroker(drains=[_reset])

    _run(monkeypatch, broker, sleeps_until_stop=1)

    assert ("basic_qos", 0, 1, False) in broker.events
    assert ("basic_consume", "jobs", False) in broker.events


def test_connection_reset_reattaches_on_a_new_connection(monkeypatch):
    broker = FakeBroker(drains=[_reset, _reset])

    _run(monkeypatch, broker, sleeps_until_stop=2)

    first, second = broker.connections
    assert first.closed
    assert second.channel.on_message is not None
    assert [e[0] for e in broker.events].count("basic_consume") == 2
    assert [e[0] for e in broker.events].count("queue_declare") == 2


def test_drains_only_the_connection_it_consumes_on(monkeypatch):
    drained = []

    def deliver_once_then_reset(connection):
        drained.append(connection)
        if len(drained) > 1:
            raise ConnectionResetError()

    broker = FakeBroker(drains=[deliver_once_then_reset])

    _run(monkeypatch, broker, sleeps_until_stop=1)

    assert ("drain", "bare") not in broker.events
    assert len(drained) == 2


def test_a_handler_base_exception_does_not_end_the_consumer(monkeypatch):
    def cancelled(body):
        raise asyncio.CancelledError()

    def deliver(connection):
        connection.channel.on_message(_message(connection.channel, "scan", tag=4))

    broker = FakeBroker(drains=[deliver, _reset])

    _run(monkeypatch, broker, sleeps_until_stop=2, callback=cancelled)

    first, second = broker.connections
    assert ("ack", 4) in broker.events
    assert first.closed
    assert second.channel.on_message is not None


def test_unreachable_broker_is_retried_forever_with_capped_backoff(monkeypatch):
    broker = FakeBroker(drains=[None] * 8 + [_reset], connect_failures=8)

    sleeps = _run(monkeypatch, broker, sleeps_until_stop=9)

    assert sleeps == [1, 2, 4, 8, 16, 30, 30, 30, 1]
    assert broker.connections[-1].channel.on_message is not None


def test_backoff_resets_once_the_consumer_is_attached(monkeypatch):
    broker = FakeBroker(drains=[None, None, _reset], connect_failures=2)

    sleeps = _run(monkeypatch, broker, sleeps_until_stop=3)

    assert sleeps == [1, 2, 1]
