"""
OpenTelemetry metrics for K8s Collector

Tracks RabbitMQ consumer health, message processing by handler type, tenant/account, and connection stability.

Replaces the previous prometheus_client registry + `/metrics` scrape endpoint. The
collector runs inside a customer cluster, so a pull model required a Prometheus
Operator there and still left the samples in the customer's Prometheus -- nothing
forwarded them to Nudgebee. Metrics are now pushed over OTLP to whatever endpoint
`OTEL_EXPORTER_OTLP_ENDPOINT` names, matching relay-server/pkg/otel.

Metric names and label keys are unchanged from the Prometheus version so existing
dashboards and alerts keep working once the collector re-exports them.
"""

import logging
import os
import threading
import time
from contextlib import contextmanager
from typing import Dict, Iterable, Iterator, List, Sequence, Tuple

from opentelemetry import metrics
from opentelemetry.metrics import CallbackOptions, Observation
from opentelemetry.sdk.metrics.view import ExplicitBucketHistogramAggregation, View

logger = logging.getLogger(__name__)

meter = metrics.get_meter("k8s-collector")

# Bucket boundaries carried over verbatim from the prometheus_client Histograms.
_DURATION_BUCKETS: Sequence[float] = [0.1, 0.5, 1.0, 5.0, 10.0, 30.0, 60.0, 120.0, 300.0, 600.0, 1800.0]
_SIZE_BUCKETS: Sequence[float] = [1024, 10240, 102400, 1048576, 10485760, 104857600]


def otlp_insecure() -> bool:
    """Whether the gRPC OTLP channel should skip TLS.

    The endpoint we ship is a plaintext in-cluster OTLP receiver, and every chart
    writes it without a scheme (`otel-...svc:4317`). The Python SDK only applies
    the http/https convention when a scheme is present and otherwise defaults to
    TLS, so the exporter opened a TLS channel against a plaintext listener: every
    export failed with SSL_ERROR_SSL / UNAVAILABLE for the whole life of the
    process, silently, since export errors are only logged. relay-server
    hardcodes WithInsecure() against the same endpoint; this keeps the two
    consistent while still honouring an explicit https://.

    An explicit OTEL_EXPORTER_OTLP{,_METRICS}_INSECURE is the operator's call and
    wins -- resolving it here rather than deferring to the SDK keeps the
    precedence in one place, since passing `insecure=` at all overrides it.
    A var that is *present but empty* is treated as unset rather than as "false":
    Helm renders an unset value as an empty string, and reading that as "use TLS"
    would silently reintroduce the very failure this function exists to prevent.
    """
    for var in ("OTEL_EXPORTER_OTLP_METRICS_INSECURE", "OTEL_EXPORTER_OTLP_INSECURE"):
        value = os.environ.get(var, "").strip()
        if value:
            return value.lower() in ("true", "1")
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") or os.environ.get(
        "OTEL_EXPORTER_OTLP_ENDPOINT", ""
    )
    return not endpoint.strip().lower().startswith("https://")


def metric_views() -> List[View]:
    """Views that restore the explicit histogram buckets and bound cardinality.

    OTel's default histogram aggregation uses its own boundaries, so without these
    the recorded distributions would not match the Prometheus ones. Passed to the
    MeterProvider in app.py.
    """
    return [
        # tenant_id / account_id are dropped from this histogram only. They grow
        # with the customer base, and a histogram costs 14 series per attribute set
        # (12 buckets + count + sum) where a counter costs 1. Push makes that bite
        # harder than scrape did: every gunicorn worker now exports its own copy
        # under a distinct service.instance.id, whereas a scrape only ever reached
        # one of them. handler_processing_total keeps the full label set, so
        # per-tenant invocation counts and error rates are unaffected -- only
        # per-tenant *latency percentiles* are given up.
        View(
            instrument_name="k8s_collector_handler_processing_duration_seconds",
            attribute_keys={"handler_type", "action"},
            aggregation=ExplicitBucketHistogramAggregation(_DURATION_BUCKETS),
        ),
        View(
            instrument_name="rabbitmq_message_processing_duration_seconds",
            aggregation=ExplicitBucketHistogramAggregation(_DURATION_BUCKETS),
        ),
        View(
            instrument_name="rabbitmq_message_size_bytes",
            aggregation=ExplicitBucketHistogramAggregation(_SIZE_BUCKETS),
        ),
    ]


class _GaugeState:
    """Backing store for an observable gauge, keyed by attribute set.

    OTel has no synchronous settable gauge: an UpDownCounter takes deltas, while
    every caller here reports an absolute value (`set_worker_threads`,
    `set_consumer_health`, ...). Holding the current value per attribute set and
    reporting it from an observable callback preserves the prometheus_client
    `Gauge.set()` semantics exactly, and keeps `.get()` working for
    rabbitmq_client's in-flight accounting.
    """

    def __init__(self, name: str, description: str) -> None:
        self._values: Dict[Tuple[Tuple[str, str], ...], float] = {}
        self._lock = threading.Lock()
        meter.create_observable_gauge(name, callbacks=[self._observe], description=description)

    def _observe(self, options: CallbackOptions) -> Iterable[Observation]:
        with self._lock:
            return [Observation(value, dict(key)) for key, value in self._values.items()]

    @staticmethod
    def _key(attributes: Dict[str, str]) -> Tuple[Tuple[str, str], ...]:
        return tuple(sorted(attributes.items()))

    def set(self, value: float, attributes: Dict[str, str]) -> None:
        with self._lock:
            self._values[self._key(attributes)] = value

    def add(self, delta: float, attributes: Dict[str, str]) -> None:
        key = self._key(attributes)
        with self._lock:
            self._values[key] = self._values.get(key, 0.0) + delta

    def get(self, attributes: Dict[str, str]) -> float:
        with self._lock:
            return self._values.get(self._key(attributes), 0.0)


# ============================================================================
# Handler Processing Metrics (with tenant/account tracking)
# ============================================================================

handler_processing_duration_seconds = meter.create_histogram(
    "k8s_collector_handler_processing_duration_seconds",
    unit="s",
    description="Time spent processing messages by handler type, tenant, and account",
)

handler_processing_total = meter.create_counter(
    "k8s_collector_handler_processing_total",
    description="Total number of handler invocations by type, tenant, and account",
)

handler_errors_total = meter.create_counter(
    "k8s_collector_handler_errors_total",
    description="Total number of handler errors by type, tenant, and error category",
)

# ============================================================================
# RabbitMQ Connection Metrics
# ============================================================================

rabbitmq_connection_total = meter.create_counter(
    "rabbitmq_connections_total",
    description="Total number of RabbitMQ connections established",
)

rabbitmq_connection_errors_total = meter.create_counter(
    "rabbitmq_connection_errors_total",
    description="Total number of RabbitMQ connection errors",
)

rabbitmq_reconnections_total = meter.create_counter(
    "rabbitmq_reconnections_total",
    description="Total number of RabbitMQ reconnection attempts",
)

rabbitmq_connection_status = _GaugeState(
    "rabbitmq_connection_status",
    "Current status of RabbitMQ connections (1=connected, 0=disconnected)",
)

# ============================================================================
# Message Processing Metrics
# ============================================================================

rabbitmq_messages_received_total = meter.create_counter(
    "rabbitmq_messages_received_total",
    description="Total number of messages received from RabbitMQ",
)

rabbitmq_messages_processed_total = meter.create_counter(
    "rabbitmq_messages_processed_total",
    description="Total number of messages successfully processed",
)

rabbitmq_messages_failed_total = meter.create_counter(
    "rabbitmq_messages_failed_total",
    description="Total number of messages that failed processing",
)

rabbitmq_messages_dlq_total = meter.create_counter(
    "rabbitmq_messages_dlq_total",
    description="Total number of messages routed to DLQ",
)

rabbitmq_messages_dropped_disabled_total = meter.create_counter(
    "rabbitmq_messages_dropped_disabled_total",
    description="Total number of messages dropped because their cloud account is disabled",
)

rabbitmq_publish_failures_total = meter.create_counter(
    "rabbitmq_publish_failures_total",
    description="Total number of messages that could not be published to RabbitMQ",
)

rabbitmq_message_processing_duration_seconds = meter.create_histogram(
    "rabbitmq_message_processing_duration_seconds",
    unit="s",
    description="Time spent processing messages end-to-end",
)

rabbitmq_messages_in_flight = _GaugeState(
    "rabbitmq_messages_in_flight",
    "Number of messages currently being processed",
)

rabbitmq_message_size_bytes = meter.create_histogram(
    "rabbitmq_message_size_bytes",
    unit="By",
    description="Size of messages in bytes",
)

# ============================================================================
# Thread Pool Metrics
# ============================================================================

rabbitmq_worker_threads_active = _GaugeState(
    "rabbitmq_worker_threads_active",
    "Number of active worker threads processing messages",
)

rabbitmq_worker_threads_total = _GaugeState(
    "rabbitmq_worker_threads_total",
    "Total number of worker threads in the pool",
)

# ============================================================================
# ACK/NACK Metrics
# ============================================================================

rabbitmq_acks_total = meter.create_counter(
    "rabbitmq_acks_total",
    description="Total number of message acknowledgements sent",
)

rabbitmq_nacks_total = meter.create_counter(
    "rabbitmq_nacks_total",
    description="Total number of message rejections sent",
)

# ============================================================================
# Consumer Health
# ============================================================================

consumer_health = _GaugeState(
    "rabbitmq_consumer_health",
    "Health status of RabbitMQ consumers (1=healthy, 0=unhealthy)",
)

topology_declarations_total = meter.create_counter(
    "rabbitmq_topology_declarations_total",
    description="Total number of topology declarations (exchange/queue creation)",
)

# ============================================================================
# Helper Functions
# ============================================================================


def record_handler_processing(
    handler_type: str,
    tenant_id: str,
    account_id: str,
    action: str,
    duration_seconds: float,
    success: bool,
):
    """Record handler processing metrics"""
    status = "success" if success else "failed"
    handler_processing_duration_seconds.record(
        duration_seconds,
        {
            "handler_type": handler_type,
            "tenant_id": tenant_id,
            "account_id": account_id,
            "action": action,
        },
    )
    handler_processing_total.add(
        1,
        {
            "handler_type": handler_type,
            "tenant_id": tenant_id,
            "account_id": account_id,
            "action": action,
            "status": status,
        },
    )
    logger.debug(
        f"Metrics: {handler_type}/{action} for tenant={tenant_id}, account={account_id} "
        f"took {duration_seconds:.2f}s, status={status}"
    )


def record_handler_error(handler_type: str, tenant_id: str, account_id: str, error_type: str):
    """Record handler error"""
    handler_errors_total.add(
        1,
        {
            "handler_type": handler_type,
            "tenant_id": tenant_id,
            "account_id": account_id,
            "error_type": error_type,
        },
    )


@contextmanager
def track_handler(handler_type: str, tenant_id: object, account_id: object, action: str) -> Iterator[None]:
    """Time a handler run and record its outcome, re-raising any exception.

    The rabbitmq_* metrics only say a queue is being consumed; this is what says
    which handler is slow or failing, and for which account. Exceptions are
    re-raised untouched so the consumer's retry / DLQ path is unchanged.
    """
    # Coerced because ids come straight off message payloads (possibly missing),
    # and OTel drops non-primitive attribute values with only a warning.
    tenant, account = str(tenant_id or ""), str(account_id or "")
    start = time.monotonic()
    try:
        yield
    except Exception as e:
        record_handler_error(handler_type, tenant, account, type(e).__name__)
        record_handler_processing(handler_type, tenant, account, action, time.monotonic() - start, False)
        raise
    record_handler_processing(handler_type, tenant, account, action, time.monotonic() - start, True)


def record_connection_established(queue_name: str, connection_type: str = "consumer"):
    """Record a new connection establishment"""
    rabbitmq_connection_total.add(1, {"queue_name": queue_name, "connection_type": connection_type})
    rabbitmq_connection_status.set(1, {"queue_name": queue_name})
    logger.debug(f"Metrics: Connection established for {queue_name}")


def record_connection_error(queue_name: str, error_type: str):
    """Record a connection error"""
    rabbitmq_connection_errors_total.add(1, {"queue_name": queue_name, "error_type": error_type})
    rabbitmq_connection_status.set(0, {"queue_name": queue_name})
    logger.debug(f"Metrics: Connection error for {queue_name}: {error_type}")


def record_reconnection(queue_name: str, success: bool):
    """Record a reconnection attempt"""
    rabbitmq_reconnections_total.add(1, {"queue_name": queue_name, "success": str(success).lower()})
    if success:
        rabbitmq_connection_status.set(1, {"queue_name": queue_name})
    logger.debug(f"Metrics: Reconnection {'succeeded' if success else 'failed'} for {queue_name}")


def record_message_received(queue_name: str, message_size_bytes: int = 0):
    """Record a message reception"""
    rabbitmq_messages_received_total.add(1, {"queue_name": queue_name})
    if message_size_bytes > 0:
        rabbitmq_message_size_bytes.record(message_size_bytes, {"queue_name": queue_name})
    logger.debug(f"Metrics: Message received from {queue_name} ({message_size_bytes} bytes)")


def record_message_processed(queue_name: str, duration_seconds: float):
    """Record successful message processing"""
    rabbitmq_messages_processed_total.add(1, {"queue_name": queue_name})
    rabbitmq_message_processing_duration_seconds.record(duration_seconds, {"queue_name": queue_name})
    logger.debug(f"Metrics: Message processed from {queue_name} in {duration_seconds:.2f}s")


def record_message_failed(queue_name: str, reason: str):
    """Record message processing failure"""
    rabbitmq_messages_failed_total.add(1, {"queue_name": queue_name, "reason": reason})
    logger.debug(f"Metrics: Message failed from {queue_name}: {reason}")


def record_publish_failed(routing_key: str, reason: str):
    """Record a publish that never reached the broker.

    The only producer-side signal this service has: every other rabbitmq_*
    metric is recorded by the consumer, so a broker outage on the ingestion
    path used to be invisible except as generic HTTP 5xx.
    """
    rabbitmq_publish_failures_total.add(1, {"routing_key": routing_key, "reason": reason})
    logger.debug(f"Metrics: Publish failed to {routing_key}: {reason}")


def record_message_dlq(queue_name: str):
    """Record message sent to DLQ"""
    rabbitmq_messages_dlq_total.add(1, {"queue_name": queue_name})
    logger.debug(f"Metrics: Message sent to DLQ from {queue_name}")


def record_message_dropped_disabled(queue_name: str, account_id: str):
    """Record a message dropped because its cloud account is disabled"""
    rabbitmq_messages_dropped_disabled_total.add(1, {"queue_name": queue_name, "account_id": account_id})
    logger.debug(f"Metrics: Message dropped from {queue_name}, account {account_id} is disabled")


def record_ack(queue_name: str, success: bool):
    """Record message acknowledgement"""
    status = "success" if success else "failed"
    rabbitmq_acks_total.add(1, {"queue_name": queue_name, "status": status})


def record_nack(queue_name: str, requeue: bool):
    """Record message rejection"""
    rabbitmq_nacks_total.add(1, {"queue_name": queue_name, "requeue": str(requeue).lower()})


def set_messages_in_flight(queue_name: str, count: float):
    """Set the number of messages currently being processed"""
    rabbitmq_messages_in_flight.set(count, {"queue_name": queue_name})


def set_worker_threads(queue_name: str, active: float, total: float):
    """Set worker thread counts"""
    rabbitmq_worker_threads_active.set(active, {"queue_name": queue_name})
    rabbitmq_worker_threads_total.set(total, {"queue_name": queue_name})


def record_topology_declaration(queue_name: str, success: bool):
    """Record topology declaration attempt"""
    status = "success" if success else "failed"
    topology_declarations_total.add(1, {"queue_name": queue_name, "status": status})


def set_consumer_health(queue_name: str, healthy: bool):
    """Set consumer health status"""
    consumer_health.set(1 if healthy else 0, {"queue_name": queue_name})


class LabeledGaugeWrapper:
    """Wrapper around a labeled observable gauge for convenient inc/dec/get operations."""

    def __init__(self, gauge: _GaugeState, label_kwargs: Dict[str, str]):
        self._gauge = gauge
        self._attributes = label_kwargs

    def inc(self, amount: int = 1) -> None:
        self._gauge.add(amount, self._attributes)

    def dec(self, amount: int = 1) -> None:
        self._gauge.add(-amount, self._attributes)

    def set(self, value: int) -> None:
        self._gauge.set(value, self._attributes)

    def get(self) -> float:
        return self._gauge.get(self._attributes)


_consumer_active_tasks_gauge = _GaugeState(
    "rabbitmq_consumer_active_tasks",
    "Number of tasks currently being processed by a specific consumer",
)

_consumer_active_tasks_gauges: Dict[str, LabeledGaugeWrapper] = {}
_consumer_active_tasks_gauges_lock = threading.Lock()


def get_consumer_active_tasks_gauge(queue_name: str) -> LabeledGaugeWrapper:
    """Get or create a gauge handle for tracking active tasks for a specific consumer queue."""
    with _consumer_active_tasks_gauges_lock:
        if queue_name not in _consumer_active_tasks_gauges:
            _consumer_active_tasks_gauges[queue_name] = LabeledGaugeWrapper(
                _consumer_active_tasks_gauge, {"queue_name": queue_name}
            )
        return _consumer_active_tasks_gauges[queue_name]
