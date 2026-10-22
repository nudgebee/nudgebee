"""Broker reconnects must not accumulate consumers on the notifications queue.

Needs a live RabbitMQ. Set RABBIT_MQ_TEST_HOST (and optionally RABBIT_MQ_TEST_PORT,
RABBIT_MQ_TEST_USERNAME, RABBIT_MQ_TEST_PASSWORD) to run it; it is skipped otherwise.
"""

import asyncio
import os
import uuid

import aio_pika
import pytest

from notifications_server.rabbitmq.message_consumer import Consumer

HOST = os.environ.get("RABBIT_MQ_TEST_HOST")
PORT = int(os.environ.get("RABBIT_MQ_TEST_PORT", "5672"))
USERNAME = os.environ.get("RABBIT_MQ_TEST_USERNAME", "guest")
PASSWORD = os.environ.get("RABBIT_MQ_TEST_PASSWORD", "guest")

pytestmark = pytest.mark.skipif(not HOST, reason="requires a live RabbitMQ; set RABBIT_MQ_TEST_HOST")


def _params():
    return dict(host=HOST, port=PORT, login=USERNAME, password=PASSWORD)


async def _consumer_count(queue: str) -> int:
    conn = await aio_pika.connect(**_params())
    try:
        channel = await conn.channel()
        declared = await channel.declare_queue(queue, durable=True, passive=True)
        return declared.declaration_result.consumer_count
    finally:
        await conn.close()


async def _wait_consuming(queue: str) -> None:
    for _ in range(100):
        try:
            if await _consumer_count(queue) == 1:
                return
        except aio_pika.exceptions.AMQPError:
            pass  # queue not declared yet
        await asyncio.sleep(0.2)
    raise AssertionError("consumer never started consuming")


async def _drop_connection(consumer: Consumer) -> None:
    # Close the AMQP transport underneath the robust connection without calling
    # close() on it: from the client's side this is what a broker restart looks like.
    conn = consumer._connection
    attempts_before = conn.connection_attempt
    await conn.transport.connection.close(ConnectionResetError("simulated broker restart"))
    for _ in range(600):
        await asyncio.sleep(0.1)
        if conn.connection_attempt > attempts_before and not conn.reconnecting and conn.transport is not None:
            break
    else:
        raise AssertionError("robust connection did not reconnect")
    await asyncio.sleep(1)


async def _cleanup(queue: str) -> None:
    # Best effort: a cleanup failure must not mask the assertion that failed the test.
    try:
        conn = await aio_pika.connect(**_params())
        try:
            channel = await conn.channel()
            await channel.queue_delete(queue)
            await channel.queue_delete(queue + "_delayed_v2")
            await channel.queue_delete(queue + "_dlq")
            await channel.exchange_delete(queue + "_exchange")
        finally:
            await conn.close()
    except Exception:
        pass


async def _run_scenario() -> None:
    queue = f"test_consumer_reconnect_{uuid.uuid4().hex[:8]}"
    delivered = []

    async def on_message(headers, body):
        delivered.append(body)

    consumer = Consumer(queue, HOST, PORT, USERNAME, PASSWORD)
    run_task = asyncio.create_task(consumer.run(on_message))
    try:
        await _wait_consuming(queue)

        for _ in range(2):
            await _drop_connection(consumer)
            assert await _consumer_count(queue) == 1

        await consumer.publish_message({"probe": 1})
        for _ in range(50):
            if delivered:
                break
            await asyncio.sleep(0.1)
        assert delivered, "consumer did not receive a message after reconnecting"
    finally:
        await consumer.stop()
        run_task.cancel()
        try:
            await run_task
        except asyncio.CancelledError:
            pass
        await _cleanup(queue)


def test_consumer_count_stays_one_across_reconnects():
    asyncio.run(_run_scenario())
