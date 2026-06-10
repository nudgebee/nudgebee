import logging
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Callable, Optional

from kombu import Connection, Exchange, Queue, Message
from kombu.mixins import ConsumerMixin
from kombu.pools import producers

from config import Configs
from metrics import prometheus_metrics

logger = logging.getLogger(__name__)
LARGE_MESSAGE_THRESHOLD_BYTES = 100 * 1024 * 1024


def _get_connection_string() -> str:
    host = Configs.RABBIT_MQ_HOST
    port = Configs.RABBIT_MQ_PORT
    user = Configs.RABBIT_MQ_USERNAME
    pwd = Configs.RABBIT_MQ_PASSWORD
    vhost = "/"
    return f"amqp://{user}:{pwd}@{host}:{port}/{vhost}"


# Shared connection for producer pool
_shared_connection = None
_connection_lock = threading.Lock()


def get_connection() -> Connection:
    """
    Get a shared connection for use with producer pool.
    Connection is reused across all publish operations.
    """
    global _shared_connection
    with _connection_lock:
        if _shared_connection is None:
            _shared_connection = Connection(
                _get_connection_string(),
                heartbeat=60,  # Increased from 30s for better stability
                transport_options={
                    "max_retries": 3,
                    "interval_start": 0,
                    "interval_step": 0.2,
                    "interval_max": 0.5,
                    "socket_timeout": 30,  # Socket read/write timeout
                    "read_timeout": 30,  # Read timeout for blocking operations
                    "write_timeout": 30,  # Write timeout
                },
            )
            logger.info("Created shared RabbitMQ connection for producer pool")
        return _shared_connection


def publish_message(
    exchange_name: str,
    routing_key: str,
    message: Any,
    exchange_type: str = "direct",
    message_ttl: Optional[int] = None,  # TTL in milliseconds
) -> None:
    """
    Publish message with optional TTL using producer pool.

    Args:
        exchange_name
        routing_key
        message
        exchange_type
        message_ttl: Time-to-live in milliseconds. If None, uses default.
    """
    exchange = Exchange(exchange_name, type=exchange_type, durable=True, auto_delete=False)

    # Get shared connection for producer pool
    connection = get_connection()

    try:
        # Prepare message properties
        properties = {}
        if message_ttl:
            # kombu's publish() expects expiration in seconds (it multiplies by 1000 internally).
            # Passing a string causes Python string repetition instead of numeric multiplication,
            # producing a ~7000-digit integer that exceeds Python 3.11+ str(int) conversion limits.
            properties["expiration"] = message_ttl / 1000.0

        # Use producer pool - it handles connection reuse and recovery
        with producers[connection].acquire(block=True) as producer:
            producer.publish(
                message,
                exchange=exchange,
                routing_key=routing_key,
                declare=[exchange],
                retry=True,
                retry_policy={
                    "max_retries": 3,
                    "interval_start": 0,
                    "interval_step": 0.2,
                    "interval_max": 0.5,
                },
                **properties,
            )
            logger.debug("Published message to %s/%s", exchange_name, routing_key)
    except Exception as e:
        logger.exception("Failed to publish to %s/%s: %s", exchange_name, routing_key, e)
        raise
    # DO NOT close connection - let the pool manage it


class RabbitConsumer(ConsumerMixin):
    """
    A robust ConsumerMixin-based RabbitMQ consumer with connection recovery.
    """

    def __init__(
        self,
        exchange_name: str,
        queue_name: str,
        routing_key: str,
        callback: Callable[[Any], None],
        exchange_type: str = "direct",
        message_ttl: Optional[int] = None,  # TTL in milliseconds
        message_max_retries: int = 3,  # Only for message processing failures
        retry_delay: float = 5.0,
        max_workers: int = 5,  # Max concurrent message processing threads
    ):
        self.exchange_name = exchange_name
        self.queue_name = queue_name
        self.routing_key = routing_key
        self.exchange_type = exchange_type
        self.callback = callback
        self.message_ttl = message_ttl
        self.message_max_retries = message_max_retries
        self.retry_delay = retry_delay
        self.max_workers = max_workers
        self._should_stop = threading.Event()

        # Thread pool for processing messages in background
        # This keeps the main consumer loop free to send heartbeats
        self._executor = ThreadPoolExecutor(max_workers=max_workers, thread_name_prefix=f"rmq-worker-{queue_name}")

        # Initialize worker thread metrics
        self._active_tasks_gauge = prometheus_metrics.get_consumer_active_tasks_gauge(self.queue_name)
        prometheus_metrics.set_worker_threads(self.queue_name, 0, self.max_workers)

        # Create dedicated connection for this consumer
        # Consumers need separate connections from producers
        # Heartbeats are sent by the main consumer thread during drain_events(), NOT in a
        # background thread. GIL contention from worker threads can starve the main thread,
        # causing missed heartbeats. Configurable via K8S_COLLECTOR_CONSUMER_HEARTBEAT.
        from config import Configs

        heartbeat = Configs.K8S_COLLECTOR_CONSUMER_HEARTBEAT
        self.connection = Connection(
            _get_connection_string(),
            heartbeat=heartbeat,
            transport_options={
                "max_retries": 3,
                "interval_start": 0,
                "interval_step": 0.2,
                "interval_max": 0.5,
            },
        )

        # Declare topology once at startup
        self._declare_topology()

    def _declare_topology(self):
        """Declare exchanges, queues, and bindings with infinite retry"""
        attempt = 0
        while True:  # Infinite retry for connection issues
            try:
                attempt += 1
                logger.info("Declaring topology (attempt %d)...", attempt)

                with self.connection.channel() as channel:
                    # Main exchange
                    main_ex = Exchange(self.exchange_name, type=self.exchange_type, durable=True, auto_delete=False)
                    main_ex.declare(channel=channel)

                    # Dead-letter exchange
                    dlx_name = f"{self.exchange_name}_dlx"
                    dlx_ex = Exchange(dlx_name, type="direct", durable=True, auto_delete=False)
                    dlx_ex.declare(channel=channel)

                    # Queue arguments
                    queue_args = {
                        "x-dead-letter-exchange": dlx_name,
                        "x-dead-letter-routing-key": self.routing_key,
                    }

                    # Add message TTL if specified
                    if self.message_ttl:
                        queue_args["x-message-ttl"] = self.message_ttl

                    # Main queue with DLX args
                    main_queue = Queue(
                        name=self.queue_name,
                        exchange=main_ex,
                        routing_key=self.routing_key,
                        durable=True,
                        exclusive=False,
                        auto_delete=False,
                        arguments=queue_args,
                    )
                    main_queue.declare(channel=channel)

                    # DLQ bound to DLX
                    dlq = Queue(
                        name=f"{self.queue_name}.dlq",
                        exchange=dlx_ex,
                        routing_key=self.routing_key,
                        durable=True,
                        exclusive=False,
                        auto_delete=False,
                    )
                    dlq.declare(channel=channel)

                    logger.info("Topology declared successfully")
                    prometheus_metrics.record_topology_declaration(self.queue_name, success=True)
                    return

            except Exception as e:
                logger.warning("Failed to declare topology (attempt %d): %s", attempt, e)
                prometheus_metrics.record_topology_declaration(self.queue_name, success=False)
                logger.info("Retrying topology declaration in %.1f seconds...", self.retry_delay)
                time.sleep(self.retry_delay)

    def get_consumers(self, Consumer, channel):
        """Set up the consumer with proper topology reference"""
        # Use same exchange/queue definitions as in topology declaration
        main_ex = Exchange(self.exchange_name, type=self.exchange_type, durable=True, auto_delete=False)

        # Reference the already-declared queue
        queue_args = {
            "x-dead-letter-exchange": f"{self.exchange_name}_dlx",
            "x-dead-letter-routing-key": self.routing_key,
        }
        if self.message_ttl:
            queue_args["x-message-ttl"] = self.message_ttl

        q = Queue(
            name=self.queue_name,
            exchange=main_ex,
            routing_key=self.routing_key,
            durable=True,
            exclusive=False,
            auto_delete=False,
            arguments=queue_args,
        )

        return [
            Consumer(
                queues=[q],
                callbacks=[self._on_message],
                no_ack=False,  # Manual ack: ensure message is processed before acking
                accept=["application/json", "application/octet-stream"],
                # Prefetch multiple messages so thread pool can process concurrently
                # while main loop continues sending heartbeats
                prefetch_count=self.max_workers,
            )
        ]

    def _force_reconnect(self):
        """Force-close the connection to trigger kombu's reconnection logic.

        When ACK/reject fails because the connection is dead, the unacked message
        permanently occupies a prefetch slot. Since prefetch_count is small (equal to
        max_workers), a few stuck slots can block all message delivery. Closing the
        connection forces kombu's ConsumerMixin main loop to detect the error and
        reconnect, which releases all stuck prefetch slots on the broker side.

        Thread-safety: called from worker threads while the main thread runs
        drain_events(). Closing the underlying transport is safe — it will cause
        drain_events() to raise a connection error, which ConsumerMixin catches
        and handles by reconnecting.
        """
        try:
            self.connection.close()
        except Exception:
            pass

    def _process_message_in_thread(self, body: Any, message: Message) -> None:
        """Process message in background thread with retry logic.

        Note: Using manual ack mode (no_ack=False) to ensure message is processed
        before acking. This allows RabbitMQ to requeue messages on failure.

        ACK/reject failures trigger a forced reconnect to free stuck prefetch slots.
        Handlers are idempotent (ON CONFLICT upserts), so redelivery after reconnect
        is safe.
        """
        start_time = time.time()
        retry_count = 0

        # Track in-flight messages
        prometheus_metrics.set_messages_in_flight(
            self.queue_name,
            self._active_tasks_gauge.get(),  # Use the gauge's current value
        )

        try:
            while retry_count < self.message_max_retries:
                try:
                    logger.debug("Processing message (attempt %d)", retry_count + 1)
                    self.callback(body)
                except Exception:
                    retry_count += 1
                    logger.exception("Message processing failed (attempt %d/%d)", retry_count, self.message_max_retries)
                    prometheus_metrics.record_message_failed(self.queue_name, "processing_error")

                    if retry_count < self.message_max_retries:
                        time.sleep(self.retry_delay)
                    else:
                        logger.error("Max message processing retries exceeded (rejecting message)")
                        try:
                            message.reject(requeue=False)
                            prometheus_metrics.record_message_dlq(self.queue_name)
                            prometheus_metrics.record_nack(self.queue_name, requeue=False)
                        except Exception:
                            logger.warning(
                                "Reject failed (connection closed), forcing reconnect to free prefetch slots"
                            )
                            self._force_reconnect()
                        return
                    continue

                # Callback succeeded — now try to ACK
                duration = time.time() - start_time
                try:
                    message.ack()
                    prometheus_metrics.record_ack(self.queue_name, success=True)
                except Exception:
                    logger.warning(
                        "ACK failed (connection closed) after successful processing. "
                        "Duration: %.2fs. Forcing reconnect to free prefetch slots. "
                        "Message will be redelivered and handled idempotently.",
                        duration,
                    )
                    prometheus_metrics.record_ack(self.queue_name, success=False)
                    prometheus_metrics.record_message_processed(self.queue_name, duration)
                    self._force_reconnect()
                    return

                prometheus_metrics.record_message_processed(self.queue_name, duration)
                logger.debug("Message processed successfully and acked (%.2fs)", duration)
                return
        finally:
            self._active_tasks_gauge.dec()  # Decrement gauge on completion

    def _on_message(self, body: Any, message: Message) -> None:
        """
        Non-blocking message handler - submits work to thread pool and returns immediately.
        This allows the main consumer loop to continue calling drain_events() which sends heartbeats,
        preventing connection timeouts even during long-running message processing.
        """
        try:
            # Get the size of the raw payload in bytes
            message_size_bytes = len(message.body)
            prometheus_metrics.record_message_received(self.queue_name, message_size_bytes)

            # Check if the message exceeds the 100 MB threshold
            if message_size_bytes > LARGE_MESSAGE_THRESHOLD_BYTES:
                # Calculate size in MB for a more readable log message
                size_in_mb = message_size_bytes / (1024 * 1024)
                logger.warning("Received a large message: %.2f MB. This may impact performance.", size_in_mb)
        except Exception:
            logger.exception("Could not determine message size.")

        # Track worker thread pool status
        self._active_tasks_gauge.inc()  # Increment gauge for new task
        prometheus_metrics.set_worker_threads(
            self.queue_name,
            self._active_tasks_gauge.get(),  # Use the gauge's current value
            self.max_workers,
        )

        # Submit message processing to thread pool and return immediately
        # This keeps the consumer loop responsive for heartbeats
        if not self.should_stop:
            self._executor.submit(self._process_message_in_thread, body, message)
        else:
            logger.warning("Consumer is shutting down, rejecting message")
            message.reject(requeue=True)

    def on_connection_error(self, exc, interval):
        """Handle connection errors - will retry indefinitely"""
        logger.warning("Connection error, retrying in %.1fs: %s", interval, exc)
        logger.info("Consumer will keep retrying until RabbitMQ is available...")
        prometheus_metrics.record_connection_error(self.queue_name, type(exc).__name__)
        prometheus_metrics.record_reconnection(self.queue_name, success=False)

    def on_connection_revived(self):
        """Called when connection is restored"""
        logger.info("Connection restored successfully, resuming consumption")
        prometheus_metrics.record_reconnection(self.queue_name, success=True)
        prometheus_metrics.record_connection_established(self.queue_name, "consumer")
        prometheus_metrics.set_consumer_health(self.queue_name, healthy=True)

    def stop(self):
        """Gracefully stop the consumer"""
        logger.info("Stopping consumer...")
        self._should_stop.set()
        self.should_stop = True

        # Shutdown thread pool gracefully
        logger.info("Waiting for worker threads to finish...")
        self._executor.shutdown(wait=True, cancel_futures=False)
        logger.info("All worker threads stopped")

    def run(self):
        """Override run to add graceful shutdown"""
        logger.info("Starting consumer for queue: %s", self.queue_name)
        try:
            super().run()
        except KeyboardInterrupt:
            logger.info("Consumer interrupted by user")
        except Exception as e:
            logger.error("Consumer error: %s", e)
            raise
        finally:
            logger.info("Consumer stopped")


class ConsumerManager:
    """Manages multiple consumers with proper lifecycle"""

    def __init__(self):
        self.consumers = {}  # dict[str, tuple[RabbitConsumer, threading.Thread]]
        self._lock = threading.Lock()

    def start_consumer(
        self,
        consumer_id: str,
        exchange_name: str,
        queue_name: str,
        routing_key: str,
        callback: Callable[[Any], None],
        exchange_type: str = "direct",
        message_ttl: Optional[int] = None,
        message_max_retries: int = 3,
        max_workers: int = 5,
    ) -> None:
        """Start a consumer in a managed thread"""

        with self._lock:
            if consumer_id in self.consumers:
                logger.warning("Consumer %s already exists", consumer_id)
                return

            consumer = RabbitConsumer(
                exchange_name=exchange_name,
                queue_name=queue_name,
                routing_key=routing_key,
                callback=callback,
                exchange_type=exchange_type,
                message_ttl=message_ttl,
                message_max_retries=message_max_retries,
                max_workers=max_workers,
            )

            # Use non-daemon thread so it doesn't die with main process
            thread = threading.Thread(target=consumer.run, daemon=False)
            thread.start()

            self.consumers[consumer_id] = (consumer, thread)
            logger.info("Started consumer %s", consumer_id)

    def stop_consumer(self, consumer_id: str, timeout: float = 10.0) -> None:
        """Stop a specific consumer"""
        with self._lock:
            if consumer_id not in self.consumers:
                logger.warning("Consumer %s not found", consumer_id)
                return

            consumer, thread = self.consumers[consumer_id]
            consumer.stop()
            thread.join(timeout=timeout)

            if thread.is_alive():
                logger.warning("Consumer %s did not stop gracefully", consumer_id)
            else:
                logger.info("Consumer %s stopped", consumer_id)

            del self.consumers[consumer_id]

    def stop_all(self, timeout: float = 10.0) -> None:
        """Stop all consumers"""
        consumer_ids = list(self.consumers.keys())
        for consumer_id in consumer_ids:
            self.stop_consumer(consumer_id, timeout)


# Global consumer manager instance
consumer_manager = ConsumerManager()


def consume_message(
    exchange_name: str,
    queue_name: str,
    routing_key: str,
    callback: Callable[[Any], None],
    exchange_type: str = "direct",
    message_ttl: Optional[int] = 1000 * 60 * 60,  # TTL in milliseconds
    message_max_retries: int = 1,  # Only for message processing failures
    consumer_id: Optional[str] = None,
    max_workers: int = 5,  # Max concurrent message processing threads
) -> str:
    """
    Start a consumer using the global manager.

    Args:
        exchange_name
        queue_name
        routing_key
        callback
        exchange_type
        message_ttl: Time-to-live for messages in milliseconds
        message_max_retries: Max retries for message processing failures only
        consumer_id: Unique identifier for the consumer
        max_workers: Maximum number of concurrent worker threads for processing messages

    Returns:
        Consumer ID for management
    """
    if consumer_id is None:
        consumer_id = f"{exchange_name}_{queue_name}_{routing_key}"

    consumer_manager.start_consumer(
        consumer_id=consumer_id,
        exchange_name=exchange_name,
        queue_name=queue_name,
        routing_key=routing_key,
        callback=callback,
        exchange_type=exchange_type,
        message_ttl=message_ttl,
        message_max_retries=message_max_retries,
        max_workers=max_workers,
    )

    return consumer_id


def stop_consumer(consumer_id: str) -> None:
    """Stop a specific consumer"""
    consumer_manager.stop_consumer(consumer_id)


def stop_all_consumers() -> None:
    """Stop all consumers"""
    consumer_manager.stop_all()
