import asyncio
from unittest.mock import AsyncMock, call

from aio_pika import ExchangeType
from notifications_server.configs.settings import settings
from notifications_server.rabbitmq.message_consumer import Consumer


async def _assert_delayed_message_route():
    consumer = Consumer("notifications", "host", 5672, "user", "password")
    consumer._channel = AsyncMock()
    main_queue = AsyncMock()
    delayed_queue = AsyncMock()
    consumer._channel.declare_queue.side_effect = [main_queue, delayed_queue]
    consumer.start_consuming = AsyncMock()

    await consumer.setup_exchange()

    consumer._channel.declare_exchange.assert_has_awaits(
        [
            call("notifications_exchange", ExchangeType.DIRECT, durable=True),
            call("dlx", ExchangeType.DIRECT, durable=True),
        ]
    )
    main_queue.bind.assert_has_awaits(
        [
            call("notifications_exchange", routing_key="notifications"),
            call("dlx", routing_key="notifications"),
        ]
    )
    delayed_queue.bind.assert_not_awaited()
    consumer._channel.declare_queue.assert_has_awaits(
        [
            call("notifications", durable=True),
            call(
                "notifications_delayed",
                durable=True,
                arguments={
                    "x-message-ttl": settings.rabbitmq.dead_letter_delay_ms,
                    "x-dead-letter-exchange": "dlx",
                    "x-dead-letter-routing-key": "notifications",
                },
            ),
        ]
    )


def test_delayed_messages_dead_letter_to_main_queue_exchange():
    asyncio.run(_assert_delayed_message_route())
