"""A queue setup failure after the broker connection opens must be retried, not left
as an open connection with no consumer."""

import asyncio
import os
import uuid

import aio_pika
import pytest

from notifications_server.rabbitmq import message_consumer
from notifications_server.rabbitmq.message_consumer import Consumer

_real_sleep = asyncio.sleep


class FakeQueue:
    def __init__(self, channel):
        self.channel = channel

    async def bind(self, exchange):
        pass

    async def consume(self, callback, no_ack):
        self.channel.consumers += 1
        return "ctag"


class FakeChannel:
    def __init__(self, fail_delayed_declare):
        self.fail_delayed_declare = fail_delayed_declare
        self.consumers = 0

    async def set_qos(self, prefetch_count):
        pass

    async def declare_exchange(self, name, kind, durable):
        pass

    async def declare_queue(self, name, durable, arguments=None, robust=True):
        if name.endswith("_delayed") and self.fail_delayed_declare:
            raise aio_pika.exceptions.ChannelPreconditionFailed("inequivalent arg 'x-message-ttl'")
        return FakeQueue(self)


class FakeCallbacks:
    def add(self, callback):
        pass


class FakeConnection:
    def __init__(self, fail_delayed_declare):
        self.channel_obj = FakeChannel(fail_delayed_declare)
        self.is_closed = False
        self.reconnect_callbacks = FakeCallbacks()
        self.close_callbacks = FakeCallbacks()

    async def channel(self):
        return self.channel_obj

    async def close(self):
        self.is_closed = True


@pytest.fixture
def broker(monkeypatch):
    # The first connection hits a queue declaration error; later ones succeed.
    connections = []

    async def connect_robust(**kwargs):
        connection = FakeConnection(fail_delayed_declare=not connections)
        connections.append(connection)
        return connection

    monkeypatch.setattr(message_consumer.aio_pika, "connect_robust", connect_robust)
    return connections


def test_setup_failure_closes_and_drops_the_connection(broker):
    consumer = Consumer("notifications", "localhost", 5672, "guest", "guest")

    asyncio.run(consumer.connect())

    assert broker[0].is_closed
    assert broker[0].channel_obj.consumers == 0
    assert consumer._connection is None


def test_run_loop_retries_setup_until_consuming(broker, monkeypatch):
    consumer = Consumer("notifications", "localhost", 5672, "guest", "guest")

    async def fast_sleep(_):
        if any(c.channel_obj.consumers for c in broker):
            consumer._stopped = True
        await _real_sleep(0)

    monkeypatch.setattr(message_consumer.asyncio, "sleep", fast_sleep)

    async def on_message(headers, body):
        pass

    asyncio.run(asyncio.wait_for(consumer.run(on_message), timeout=5))

    assert len(broker) == 2
    assert broker[0].is_closed
    assert broker[1].channel_obj.consumers == 1
    assert consumer._connection is broker[1]


async def _live_scenario(host, port, user, password):
    params = dict(host=host, port=port, login=user, password=password)
    queue = f"test_consumer_setup_retry_{uuid.uuid4().hex[:8]}"

    async def consumer_count():
        conn = await aio_pika.connect(**params)
        try:
            channel = await conn.channel()
            declared = await channel.declare_queue(queue, durable=True, passive=True)
            return declared.declaration_result.consumer_count
        except aio_pika.exceptions.ChannelNotFoundEntity:
            return 0  # queue not declared yet
        finally:
            await conn.close()

    # A delayed queue that already exists with other arguments makes the consumer's
    # redeclaration fail with PRECONDITION_FAILED, as a changed retry delay would.
    admin = await aio_pika.connect(**params)
    channel = await admin.channel()
    await channel.declare_queue(queue + "_delayed", durable=True, arguments={"x-message-ttl": 1})

    delivered = []

    async def on_message(headers, body):
        delivered.append(body)

    consumer = Consumer(queue, host, port, user, password)
    run_task = asyncio.create_task(consumer.run(on_message))
    try:
        await asyncio.sleep(2)
        assert await consumer_count() == 0

        await channel.queue_delete(queue + "_delayed")
        for _ in range(100):
            if await consumer_count() == 1:
                break
            await asyncio.sleep(0.2)
        else:
            raise AssertionError("consumer never recovered after the setup error was cleared")

        await consumer.publish_message({"probe": 1})
        for _ in range(50):
            if delivered:
                break
            await asyncio.sleep(0.1)
        assert delivered, "consumer did not receive a message after recovering"
    finally:
        await consumer.stop()
        run_task.cancel()
        try:
            await run_task
        except asyncio.CancelledError:
            pass
        for name in (queue, queue + "_delayed"):
            await channel.queue_delete(name)
        await channel.exchange_delete(queue + "_exchange")
        await admin.close()


@pytest.mark.skipif(
    not os.environ.get("RABBIT_MQ_TEST_HOST"), reason="requires a live RabbitMQ; set RABBIT_MQ_TEST_HOST"
)
def test_live_consumer_recovers_from_a_queue_declaration_error():
    asyncio.run(
        _live_scenario(
            os.environ["RABBIT_MQ_TEST_HOST"],
            int(os.environ.get("RABBIT_MQ_TEST_PORT", "5672")),
            os.environ.get("RABBIT_MQ_TEST_USERNAME", "guest"),
            os.environ.get("RABBIT_MQ_TEST_PASSWORD", "guest"),
        )
    )
