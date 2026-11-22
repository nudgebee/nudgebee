import json
import logging
import threading
import time
from contextlib import suppress
from typing import Any, Callable

from kombu import Connection, Exchange, Producer
from kombu.exceptions import OperationalError
from opentelemetry import trace
from opentelemetry.propagate import extract
from server.utils.utils import QueueConfig

RETRY_POLICY = {
    "max_retries": 15,
    "interval_start": 0,
    "interval_step": 1,
    "interval_max": 3,
}

logger = logging.getLogger(__name__)

# Use a global producer pool
PRODUCER_POOL = {}


# Connection string fetch function
def _get_connection_string() -> str:
    rabbit_host = QueueConfig.RABBIT_MQ_HOST
    rabbit_port = QueueConfig.RABBIT_MQ_PORT
    rabbit_user = QueueConfig.RABBIT_MQ_USERNAME
    rabbit_pass = QueueConfig.RABBIT_MQ_PASSWORD
    rabbit_vhost = "/"  # default virtual host
    return f"amqp://{rabbit_user}:{rabbit_pass}@{rabbit_host}:{rabbit_port}/{rabbit_vhost}"


# Reuse existing producer, with a connection pool
def _get_producer(amqp_url: str) -> Producer:
    if amqp_url not in PRODUCER_POOL:
        connection = Connection(amqp_url)
        channel = connection.channel()
        producer = Producer(channel)
        PRODUCER_POOL[amqp_url] = producer
    return PRODUCER_POOL[amqp_url]


def publish_message(exchange_name: str, routing_key: str, message: Any, exchange_type: str = "direct") -> None:
    amqp_url = _get_connection_string()
    logger.info(f"Publishing message to queue {routing_key} exchange {exchange_name}")
    try:
        with Connection(amqp_url) as conn:
            channel = conn.channel()
            producer = Producer(channel)
            exchange = Exchange(exchange_name, type=exchange_type)

            producer.publish(
                message,
                exchange=exchange,
                routing_key=routing_key,
                declare=[exchange],
                retry=True,
                retry_policy=RETRY_POLICY,
            )
    except OperationalError as e:
        logger.exception(f"Failed to publish message to {routing_key}: {e}")


CONSUMERS = {}


# Consume messages with a thread and error handling
def consume_message(
    exchange_name: str,
    queue_name: str,
    routing_key: str,
    callback: Callable,
    exchange_type: str = "direct",
) -> None:
    if queue_name in CONSUMERS:
        raise RuntimeError(f"Consumer already exists for queue {queue_name}")
    consumer = KombuConsumerThread(_get_connection_string(), exchange_name, queue_name, routing_key, callback)
    CONSUMERS.update({queue_name: consumer})
    consumer.start()


# The broker sends a consumer its next message only after the previous one is acked, and a
# message is acked when its scan ends, so a pod that is busy is never handed more work.
PREFETCH_COUNT = 1
MAX_RECONNECT_DELAY_SECONDS = 30.0
# Set on the one copy a redelivered message is requeued as, so it is retried once, not forever.
ATTEMPT_HEADER = "x-nb-attempt"


def _describe(body: Any) -> str:
    """Names a queued job for a log line without logging its body, which can carry credentials."""
    try:
        parsed = json.loads(body)
        action = parsed.get("action_name") or "an unnamed message"
        account = parsed.get("account_id") or parsed.get("account")
        return f"{action} for account {account}" if account else f"{action}"
    except Exception:
        return "an unreadable message"


class KombuConsumerThread(threading.Thread):
    def __init__(
        self,
        amqp_url: str,
        exchange_name: str,
        queue_name: str,
        routing_key: str,
        callback: Callable,
    ):
        threading.Thread.__init__(self)
        self.daemon = True
        self.connection_string = amqp_url

        self.queue_name = queue_name
        self.exchange_name = exchange_name
        self.routing_key = routing_key
        self.callback = callback
        self.connection: Connection | None = None

    def callback_internal(self, message: Any) -> None:
        try:
            # Continue the distributed trace: extract the W3C context the
            # publisher injected into the AMQP headers and run the handler
            # inside a consumer span so downstream logs/outbound calls share
            # the same trace_id.
            ctx = extract(message.headers or {})
            with trace.get_tracer(__name__).start_as_current_span(
                "rabbitmq.consume", context=ctx, kind=trace.SpanKind.CONSUMER
            ):
                self.callback(message.body)
        except Exception as e:
            logger.exception(f"Unable to process {_describe(message.body)}", exc_info=True)
            # Retry and send to DLX in case of failure
            publish_message(
                exchange_name=self.exchange_name + "_dlx",
                routing_key=self.routing_key,
                message={"payload": message.body, "error": str(e)},
            )

    def on_message(self, message: Any) -> None:
        # A message comes back redelivered when an earlier delivery was never acked (the pod died
        # mid-scan, or the scan outran the queue's consumer timeout and is still running) and also,
        # for everything still queued, when the broker itself restarted uncleanly. So it is run once
        # more, as a copy marked with ATTEMPT_HEADER, and dropped if that copy comes back too: a scan
        # that kills its pod runs at most twice instead of looping.
        try:
            if not message.delivery_info.get("redelivered"):
                self.callback_internal(message)
            elif (message.headers or {}).get(ATTEMPT_HEADER):
                logger.warning(
                    f"Dropping {_describe(message.body)} from {self.queue_name}: its retry was redelivered too"
                )
            else:
                self.requeue_for_retry(message)
                logger.warning(f"Requeued redelivered {_describe(message.body)} on {self.queue_name} for one retry")
        finally:
            message.channel.basic_ack(message.delivery_tag)

    def requeue_for_retry(self, message: Any) -> None:
        properties = dict(message.properties)
        headers = {**(properties.pop("application_headers", None) or {}), ATTEMPT_HEADER: 2}
        retry = message.channel.prepare_message(
            message.body,
            priority=properties.pop("priority", None),
            content_type=properties.pop("content_type", None),
            content_encoding=properties.pop("content_encoding", None),
            headers=headers,
            properties=properties,
        )
        message.channel.basic_publish(retry, exchange=self.exchange_name, routing_key=self.routing_key)

    def run(self) -> None:
        delay = 1.0
        while True:
            try:
                connection = self.attach()
                delay = 1.0
                while True:
                    connection.drain_events()
            # BaseException too: a handler's CancelledError or SystemExit would otherwise end the thread
            # with its connection still open, leaving the broker a consumer that never acks.
            except BaseException:
                logger.exception(f"RabbitMQ consumer for {self.queue_name} stopped, reconnecting in {delay:.0f}s")
            self.close()
            time.sleep(delay)
            delay = min(delay * 2, MAX_RECONNECT_DELAY_SECONDS)

    def attach(self) -> Any:
        # Every attempt opens a new connection and declares everything again. Draining through the
        # old kombu Connection instead lets it reopen a bare connection with no consumer on it.
        # Heartbeats must stay off (kombu's default of 0): the scan runs on the thread that reads this
        # socket, so nothing would answer them and the broker would drop the connection mid-scan.
        # py-amqp's TCP keepalive still notices a dead broker within about 150s.
        self.connection = Connection(self.connection_string)
        self.connection.connect()
        connection = self.connection.connection
        channel = connection.channel()
        channel.exchange_declare(self.exchange_name, "direct", durable=True, auto_delete=False)
        channel.queue_declare(self.queue_name, durable=True, exclusive=False, auto_delete=False)
        channel.queue_bind(self.queue_name, self.exchange_name, self.routing_key)
        channel.basic_qos(0, PREFETCH_COUNT, False)
        channel.basic_consume(self.queue_name, callback=self.on_message, no_ack=False)
        logger.info(f"Consuming {self.queue_name}")
        return connection

    def close(self) -> None:
        if self.connection:
            with suppress(Exception):
                self.connection.close()
            self.connection = None
