import logging
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Callable, Optional

from kombu import Connection, Exchange, Queue, Message
from kombu.mixins import ConsumerMixin
from kombu.pools import producers
from opentelemetry import trace
from opentelemetry.propagate import extract

from config import Configs
from metrics import prometheus_metrics

logger = logging.getLogger(__name__)
LARGE_MESSAGE_THRESHOLD_BYTES = 100 * 1024 * 1024
MAX_CONSUMER_RESTART_BACKOFF = 60.0

# Interpreter-shutdown hook ordering is load-bearing here -- see the
# _register_atexit call at the bottom of this module for why.
try:
    from threading import _register_atexit as _register_thread_atexit
except ImportError:  # pragma: no cover - private API, present since 3.9
    _register_thread_atexit = None


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
        # Set by run() when the drain loop has exited. stop() waits on this
        # before tearing the executor down.
        self._run_finished = threading.Event()

        # Incremented every time the broker connection is replaced. AMQP delivery
        # tags are scoped to a channel, so a tag captured before a reconnect is
        # meaningless afterwards -- acking it raises PRECONDITION_FAILED (406)
        # and the broker kills the whole channel, which forces another reconnect.
        # Worker threads carry the epoch they were handed and skip ack/reject
        # when it no longer matches.
        self._connection_epoch = 0
        self._epoch_lock = threading.Lock()

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
        self.connection = self._new_connection()

        # Declare topology once at startup
        self._declare_topology()

    @staticmethod
    def _new_connection() -> Connection:
        from config import Configs

        return Connection(
            _get_connection_string(),
            heartbeat=Configs.K8S_COLLECTOR_CONSUMER_HEARTBEAT,
            transport_options={
                "max_retries": 3,
                "interval_start": 0,
                "interval_step": 0.2,
                "interval_max": 0.5,
            },
        )

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
        self._bump_epoch()
        try:
            self.connection.close()
        except Exception:
            pass

    def _bump_epoch(self) -> int:
        """Invalidate every delivery tag handed out on the previous channel."""
        with self._epoch_lock:
            self._connection_epoch += 1
            return self._connection_epoch

    def _settle(self, message: Message, epoch: int, reject: bool = False) -> bool:
        """ACK (or reject) a message, unless its channel has since been replaced.

        Returns True when the message was settled. A stale epoch means the
        connection was torn down while this thread was working: the broker has
        already requeued the delivery, and settling it here would either hit a
        dead channel or -- worse -- land a stale delivery tag on the NEW channel,
        which RabbitMQ answers with PRECONDITION_FAILED (406) and closes.
        Handlers are idempotent, so letting the redelivery run is correct.
        """
        # Read once: a reconnect between the comparison and the log line would
        # otherwise report an epoch that is not the one we decided against.
        current_epoch = self._connection_epoch
        if epoch != current_epoch:
            logger.warning(
                "Skipping %s for %s: connection was replaced during processing "
                "(epoch %d -> %d). Message will be redelivered.",
                "reject" if reject else "ack",
                self.queue_name,
                epoch,
                current_epoch,
            )
            return False
        if reject:
            message.reject(requeue=False)
        else:
            message.ack()
        return True

    def _process_message_in_thread(self, body: Any, message: Message, epoch: int) -> None:
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
                    # Continue the distributed trace: extract the W3C context the
                    # publisher injected into the AMQP headers and run the handler
                    # inside a consumer span so downstream logs / outbound calls
                    # share the same trace_id.
                    ctx = extract(message.headers or {})
                    with trace.get_tracer(__name__).start_as_current_span(
                        "rabbitmq.consume", context=ctx, kind=trace.SpanKind.CONSUMER
                    ):
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
                            if self._settle(message, epoch, reject=True):
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
                    if not self._settle(message, epoch):
                        prometheus_metrics.record_message_processed(self.queue_name, duration)
                        return
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
        try:
            self._executor.submit(self._process_message_in_thread, body, message, self._connection_epoch)
        except RuntimeError as e:
            # The executor is gone -- either stop() raced us, or the interpreter
            # is finalizing and concurrent.futures' own shutdown hook has already
            # set its global _shutdown flag.
            #
            # Do NOT reject(requeue=True) per message here. The broker redelivers
            # it immediately to this same dead consumer, so every message loops
            # reject -> redeliver -> reject as fast as the socket allows, for the
            # whole termination grace period. Stop consuming and drop the
            # connection instead: the broker requeues every unacked delivery in
            # one go, and nothing more is pulled.
            logger.warning("Executor unavailable (%s); stopping consumer and releasing unacked messages", e)
            self._active_tasks_gauge.dec()
            prometheus_metrics.set_worker_threads(
                self.queue_name,
                self._active_tasks_gauge.get(),
                self.max_workers,
            )
            self.should_stop = True
            self._should_stop.set()
            self._force_reconnect()

    def on_connection_error(self, exc, interval):
        """Handle connection errors - will retry indefinitely"""
        logger.warning("Connection error, retrying in %.1fs: %s", interval, exc)
        logger.info("Consumer will keep retrying until RabbitMQ is available...")
        prometheus_metrics.record_connection_error(self.queue_name, type(exc).__name__)
        prometheus_metrics.record_reconnection(self.queue_name, success=False)

    def on_connection_revived(self):
        """Called when connection is restored"""
        # New channel => every delivery tag issued on the old one is now invalid.
        self._bump_epoch()
        logger.info("Connection restored successfully, resuming consumption")
        prometheus_metrics.record_reconnection(self.queue_name, success=True)
        prometheus_metrics.record_connection_established(self.queue_name, "consumer")
        prometheus_metrics.set_consumer_health(self.queue_name, healthy=True)

    def stop(self, timeout: float = 10.0):
        """Gracefully stop the consumer.

        Order matters: the drain loop must leave `super().run()` BEFORE the
        executor is torn down. Shutting the executor down first leaves the
        consumer thread pulling messages it can no longer submit, which is the
        `cannot schedule new futures after shutdown` failure.
        """
        logger.info("Stopping consumer...")
        self._should_stop.set()
        self.should_stop = True

        # ConsumerMixin polls should_stop between drain_events() calls, so this
        # normally returns in well under a second.
        if not self._run_finished.wait(timeout=timeout):
            logger.warning(
                "Consumer loop for %s did not exit within %.1fs; shutting down executor anyway",
                self.queue_name,
                timeout,
            )

        # Shutdown thread pool gracefully
        logger.info("Waiting for worker threads to finish...")
        self._executor.shutdown(wait=True, cancel_futures=False)
        logger.info("All worker threads stopped")

    def run(self):
        """Override run to restart the drain loop instead of dying on error.

        A single unhandled exception used to kill this thread for good, leaving
        a pod that is up, healthy-looking, and consuming nothing. Observed twice
        as `'NoneType' object has no attribute 'drain_events'` after a reconnect.
        """
        logger.info("Starting consumer for queue: %s", self.queue_name)
        backoff = self.retry_delay
        try:
            while not self._stop_requested():
                try:
                    super().run()
                    return
                except KeyboardInterrupt:
                    logger.info("Consumer interrupted by user")
                    return
                except Exception as e:
                    if self._stop_requested():
                        return
                    logger.exception("Consumer error on %s, restarting in %.1fs: %s", self.queue_name, backoff, e)
                    prometheus_metrics.set_consumer_health(self.queue_name, healthy=False)
                    # Wait on the stop event, not time.sleep: a pod terminating
                    # mid-backoff would otherwise hold shutdown for up to
                    # MAX_CONSUMER_RESTART_BACKOFF and get SIGKILLed.
                    if self._should_stop.wait(timeout=backoff):
                        return
                    backoff = min(backoff * 2, MAX_CONSUMER_RESTART_BACKOFF)
                    # Close the old connection before replacing it, or the socket
                    # and its channels leak for the lifetime of the process. A
                    # fresh connection is cheaper than reasoning about whatever
                    # state kombu left the old one in.
                    self._force_reconnect()
                    self.connection = self._new_connection()
        finally:
            self._run_finished.set()
            logger.info("Consumer stopped")

    def _stop_requested(self) -> bool:
        return self.should_stop or self._should_stop.is_set()


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

            # Non-daemon so the thread doesn't die mid-message with the main
            # process -- _stop_consumers_at_exit() is what lets the interpreter
            # finish. Without that hook there is nothing to stop the loop, so
            # daemon threads are the only way out.
            thread = threading.Thread(target=consumer.run, daemon=_register_thread_atexit is None)
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
            consumer.stop(timeout=timeout)
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


def _stop_consumers_at_exit() -> None:
    """Stop consumers before the interpreter tears the thread pools down.

    Nothing in the process calls stop_all_consumers() on the way out: the
    container runs `gunicorn app:app`, so app.py is imported as a module and the
    SIGTERM handler it installs under `if __name__ == "__main__"` never runs.

    Registration order is load-bearing. threading._shutdown() walks its hooks in
    reversed() order and concurrent.futures.thread registers its own
    (`_python_exit`, which flips the flag that makes submit() raise) when this
    module imports ThreadPoolExecutor at the top. Registering here -- after that
    import -- therefore puts us ahead of it, so consumers stop pulling before the
    executor can become unusable. atexit.register would be too late: those hooks
    run only after threading._shutdown() has joined every non-daemon thread, and
    the consumer threads are non-daemon.

    The timeout is deliberately shorter than stop_consumer's default: this runs
    inside the pod's termination grace period (30s), sequentially across every
    consumer. It bounds the wait for each drain loop to exit, not the subsequent
    _executor.shutdown(wait=True) -- in-flight handlers are still allowed to
    finish, and a slow one can still run the pod into SIGKILL. That is the
    pre-existing trade-off and is safe: unacked messages are requeued.
    """
    try:
        consumer_manager.stop_all(timeout=3.0)
    except Exception:  # pragma: no cover - best effort during finalization
        logger.warning("Failed to stop consumers during interpreter shutdown", exc_info=True)


if _register_thread_atexit is not None:
    _register_thread_atexit(_stop_consumers_at_exit)
