import logging
import threading
import time
from typing import Any, Callable, cast

from kombu import Connection, Exchange, Message, Producer, Queue
from kombu.exceptions import OperationalError
from kombu.transport.virtual import Channel
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
    logger.info(f"Publishing message {message} to queue {routing_key} exchange {exchange_name}")
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

    def callback_internal(self, message: Message) -> None:
        try:
            self.callback(message.body)
        except Exception as e:
            logger.exception("Unable to process message - ", exc_info=True)
            # Retry and send to DLX in case of failure
            publish_message(
                exchange_name=self.exchange_name + "_dlx",
                routing_key=self.routing_key,
                message={"payload": message.body, "error": str(e)},
            )

    def run(self) -> None:
        self.connect()
        while self.connection is not None:
            try:
                with cast(Channel, self.connection.channel()) as channel:
                    channel.exchange_declare(
                        exchange=self.exchange_name, type="direct", durable=True, auto_delete=False
                    )
                    queue = Queue(
                        self.queue_name,
                        Exchange(self.exchange_name),
                        routing_key=self.routing_key,
                    )
                    channel.queue_declare(
                        queue=self.queue_name, durable=True, exclusive=False, auto_delete=False, arguments=None
                    )
                    queue.maybe_bind(channel)
                    channel.queue_bind(self.queue_name, self.exchange_name, self.routing_key)
                    queue.consume(callback=self.callback_internal, no_ack=True)
                    while True:
                        try:
                            self.connection.drain_events()
                        except (ConnectionError, OperationalError):
                            logger.exception("Connection lost, reconnecting...")
                            self.connect()
            except Exception:
                logger.exception("Connection lost, reconnecting...")
                self.connect()

    def connect(self) -> None:
        if self.connection is None or self.connection.connected is None:
            retries = 1
            max_retry = 15
            while True:
                try:
                    self.connection = Connection(self.connection_string)
                    self.connection.ensure_connection(max_retries=30)
                    break
                except Exception:
                    time.sleep(10)
                    retries += 1
                    logger.warning(f"retrying ..... {retries}")
                    if max_retry < retries:
                        logger.exception(f"Maximum retry exhausted. Retry no. {retries} gracefully exiting application")
                        raise Exception(f"Failed to connect to RabbitMQ after {retries} retries")

    def close(self) -> None:
        if self.connection:
            self.connection.close()
            self.connection = None
