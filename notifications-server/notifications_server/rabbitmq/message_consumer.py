import asyncio
import gzip
import json
import logging
import time
import zlib
from typing import Callable

import aio_pika
from aio_pika import ExchangeType, Message

from notifications_server.configs.settings import settings

LOG = logging.getLogger(__name__)


class Consumer:
    """
    RabbitMQ's consumer with automatic reconnects using FastAPI and aio-pika.
    """

    def __init__(self, queue, host, port, user, password):
        self.queue = queue
        self.exchange = queue + "_exchange"
        self.dlx_exchange = "dlx"
        self.consume_callback = None

        self.connection_parameters = {
            "host": host,
            "port": int(port),
            "login": user,
            "password": password,
            "heartbeat": settings.rabbitmq.heartbeat,
        }

        self._connection = None
        self._channel = None
        self._consuming_task = None
        self._processing_tasks = set()  # Track concurrent processing tasks

    @property
    def delayed_queue(self):
        return self.queue + "_delayed"

    async def connect(self):
        try:
            LOG.info("Connecting to RabbitMQ")
            self._connection = await aio_pika.connect_robust(
                **self.connection_parameters, timeout=settings.rabbitmq.connection_timeout
            )
            self._connection.reconnect_callbacks.add(self.on_reconnect)
            self._connection.close_callbacks.add(self.on_connection_closed)
            await self.open_channel()
            return
        except Exception as e:
            LOG.error(f"Connection attempt to rabbitmq failed: {e}")

    async def on_reconnect(self, connection: aio_pika.RobustConnection):
        LOG.info("Reconnected to RabbitMQ! Re-establishing channel and consumers...")
        try:
            self._channel = None
            await self.open_channel()
            LOG.info("Successfully re-established channel and consumers after reconnect")
        except Exception as e:
            LOG.error(f"Failed to re-establish channel after reconnect: {e}")
            # Retry after delay
            await asyncio.sleep(5)
            try:
                await self.open_channel()
                LOG.info("Retry successful - channel and consumers re-established")
            except Exception as retry_error:
                LOG.error(f"Retry failed to re-establish channel: {retry_error}")

    @staticmethod
    def on_connection_closed(connection: aio_pika.RobustConnection, exc: Exception = None):
        if exc:
            LOG.warning(f"RabbitMQ connection closed with exception: {exc}")
        else:
            LOG.info("RabbitMQ connection closed")

    async def open_channel(self):
        LOG.info("Creating a new channel")
        # Use robust channel that auto-recovers
        self._channel = await self._connection.channel()
        await self._channel.set_qos(prefetch_count=settings.rabbitmq.prefetch_count)
        await self.setup_exchange()

    async def setup_exchange(self):
        LOG.info(f"Declaring exchange {self.exchange}")
        await self._channel.declare_exchange(self.exchange, ExchangeType.DIRECT, durable=True)
        await self.setup_queue()

    async def setup_queue(self):
        LOG.info(f"Declaring queue {self.queue}")
        queue = await self._channel.declare_queue(self.queue, durable=True)
        await queue.bind(self.exchange)
        await self.setup_delayed_queue()

    async def setup_delayed_queue(self):
        LOG.info(f"Declaring delayed queue {self.delayed_queue}")
        await self._channel.declare_queue(
            self.delayed_queue,
            durable=True,
            arguments={
                "x-message-ttl": settings.rabbitmq.dead_letter_delay_ms,
                "x-dead-letter-exchange": self.dlx_exchange,
                "x-dead-letter-routing-key": self.queue,
            },
        )
        await self.start_consuming()

    async def start_consuming(self):
        LOG.info(f"Starting consuming messages from queue {self.queue}")
        queue = await self._channel.declare_queue(self.queue, durable=True, robust=True)
        consumer_tag = await queue.consume(self.on_message, no_ack=False)
        LOG.info(f"Successfully started consuming from queue {self.queue} with consumer tag: {consumer_tag}")

    @staticmethod
    def _decompress_message_body(body, headers):
        if headers and headers.get("compression") == "application/x-gzip":
            if isinstance(body, str):
                body = body.encode("utf-8")

            try:
                decompressed = gzip.decompress(body)
                LOG.info("Successfully decompressed gzipped message (%d bytes)", len(decompressed))
                return decompressed
            except Exception as e:
                LOG.debug("Not a valid gzip stream, trying zlib: %s", e)

            try:
                decompressed = zlib.decompress(body)
                LOG.info("Successfully decompressed zlib-compressed message (%d bytes)", len(decompressed))
                return decompressed
            except Exception as e:
                LOG.error("Failed to decompress with both gzip and zlib: %s", e)
                raise

        if isinstance(body, str):
            return body.encode("utf-8")
        return body

    def on_message(self, message: aio_pika.IncomingMessage):
        # Process message concurrently but handle acknowledgment properly
        task = asyncio.create_task(self._process_message_task(message))
        self._processing_tasks.add(task)
        task.add_done_callback(self._processing_tasks.discard)

    async def _process_message_task(self, message: aio_pika.IncomingMessage):
        async with message.process():
            headers = message.headers
            raw_body = message.body
            delivery_tag = message.delivery_tag
            start_time = time.time()
            LOG.info("Consumed message %s", delivery_tag)
            if self.consume_callback:
                try:
                    processed_body = self._decompress_message_body(raw_body, headers)
                    await self.consume_callback(headers, processed_body)
                except Exception as e:
                    LOG.error(f"Error processing message {delivery_tag}: {e}, message {raw_body}, headers {headers}")
            self.acknowledge_message(delivery_tag, start_time)

    @staticmethod
    def acknowledge_message(delivery_tag, start_time):
        time_taken = time.time() - start_time
        LOG.debug(f"Acknowledging message {delivery_tag}, time taken: {time_taken:.3f} seconds")

    async def run(self, consume_callback: Callable):
        self.consume_callback = consume_callback
        self._stopped = False
        while not self._stopped:
            try:
                if self._connection is None or self._connection.is_closed:
                    await self.connect()
                    if self._connection and not self._connection.is_closed:
                        LOG.info("Consumer is running and connected to RabbitMQ")
                await asyncio.sleep(5)  # Check connection status periodically
            except asyncio.CancelledError:
                LOG.info("Consumer run loop cancelled")
                raise
            except Exception as e:
                LOG.error(f"Consumer encountered an error: {e}, reconnecting in 5 seconds...")
                await asyncio.sleep(5)

    async def stop(self):
        LOG.info("Stopping consumer...")
        self._stopped = True
        # Wait for in-flight tasks to complete (with 30s timeout)
        if self._processing_tasks:
            LOG.info("Waiting for %d in-flight tasks to complete", len(self._processing_tasks))
            _, pending = await asyncio.wait(self._processing_tasks, timeout=30)
            if pending:
                LOG.warning("%d tasks did not complete within timeout", len(pending))
        if self._connection and not self._connection.is_closed:
            await self._connection.close()
            LOG.info("RabbitMQ connection closed")
        LOG.info("Consumer stopped")

    async def _publish_message(self, message, queue):
        if self._connection is None or self._connection.is_closed:
            LOG.error("Failed to publish message. RabbitMQ connection not available")
            return

        if self._channel is None or self._channel.is_closed:
            LOG.warning("Channel closed, attempting to reopen...")
            try:
                await self.open_channel()
            except Exception as e:
                LOG.error(f"Failed to reopen channel for publishing: {e}")
                return

        try:
            await self._channel.default_exchange.publish(
                Message(body=json.dumps(message).encode(), delivery_mode=aio_pika.DeliveryMode.PERSISTENT),
                routing_key=queue,
            )
        except Exception as e:
            LOG.error(f"Failed to publish message to queue {queue}: {e}")

    async def publish_message(self, message):
        await self._publish_message(message, self.queue)

    async def publish_delayed_message(self, message):
        await self._publish_message(message, self.delayed_queue)
