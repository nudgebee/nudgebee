"""
Prometheus metrics for K8s Collector

Tracks RabbitMQ consumer health, message processing by handler type, tenant/account, and connection stability.
"""

import logging
import threading
import os
from typing import Dict

from prometheus_client import Counter, Gauge, Histogram, Info

logger = logging.getLogger(__name__)

# Application info
app_info = Info("k8s_collector_app", "K8s Collector application information")
app_info.info({"version": os.environ.get("APP_VERSION", "0.0.0"), "component": "k8s-collector"})

# ============================================================================
# Handler Processing Metrics (with tenant/account tracking)
# ============================================================================

handler_processing_duration_seconds = Histogram(
    "k8s_collector_handler_processing_duration_seconds",
    "Time spent processing messages by handler type, tenant, and account",
    ["handler_type", "tenant_id", "account_id", "action"],
    buckets=[0.1, 0.5, 1.0, 5.0, 10.0, 30.0, 60.0, 120.0, 300.0, 600.0, 1800.0],  # up to 30 min
)

handler_processing_total = Counter(
    "k8s_collector_handler_processing_total",
    "Total number of handler invocations by type, tenant, and account",
    ["handler_type", "tenant_id", "account_id", "action", "status"],  # status: success|failed
)

handler_errors_total = Counter(
    "k8s_collector_handler_errors_total",
    "Total number of handler errors by type, tenant, and error category",
    ["handler_type", "tenant_id", "account_id", "error_type"],
)

# Specific handler action tracking
discovery_operations_total = Counter(
    "k8s_collector_discovery_operations_total",
    "Discovery operations by resource type and tenant",
    ["resource_type", "tenant_id", "account_id", "operation"],  # operation: create|update|delete
)

event_operations_total = Counter(
    "k8s_collector_event_operations_total",
    "Event operations by event type and tenant",
    ["event_type", "tenant_id", "account_id", "severity"],
)

telemetry_updates_total = Counter(
    "k8s_collector_telemetry_updates_total",
    "Telemetry updates by tenant and integration type",
    ["tenant_id", "account_id", "integration_type"],
)

# Resource processing metrics
resources_processed_total = Counter(
    "k8s_collector_resources_processed_total",
    "Total resources processed by type and tenant",
    ["resource_type", "tenant_id", "account_id"],
)

# ============================================================================
# RabbitMQ Connection Metrics
# ============================================================================

rabbitmq_connection_total = Counter(
    "rabbitmq_connections_total",
    "Total number of RabbitMQ connections established",
    ["queue_name", "connection_type"],  # connection_type: consumer|producer
)

rabbitmq_connection_errors_total = Counter(
    "rabbitmq_connection_errors_total",
    "Total number of RabbitMQ connection errors",
    ["queue_name", "error_type"],  # error_type: heartbeat_miss|connection_reset|broken_pipe
)

rabbitmq_reconnections_total = Counter(
    "rabbitmq_reconnections_total",
    "Total number of RabbitMQ reconnection attempts",
    ["queue_name", "success"],  # success: true|false
)

rabbitmq_connection_status = Gauge(
    "rabbitmq_connection_status",
    "Current status of RabbitMQ connections (1=connected, 0=disconnected)",
    ["queue_name"],
)

# ============================================================================
# Message Processing Metrics
# ============================================================================

rabbitmq_messages_received_total = Counter(
    "rabbitmq_messages_received_total",
    "Total number of messages received from RabbitMQ",
    ["queue_name"],
)

rabbitmq_messages_processed_total = Counter(
    "rabbitmq_messages_processed_total",
    "Total number of messages successfully processed",
    ["queue_name"],
)

rabbitmq_messages_failed_total = Counter(
    "rabbitmq_messages_failed_total",
    "Total number of messages that failed processing",
    ["queue_name", "reason"],  # reason: processing_error|connection_error|max_retries
)

rabbitmq_messages_dlq_total = Counter(
    "rabbitmq_messages_dlq_total",
    "Total number of messages routed to DLQ",
    ["queue_name"],
)

rabbitmq_message_processing_duration_seconds = Histogram(
    "rabbitmq_message_processing_duration_seconds",
    "Time spent processing messages end-to-end",
    ["queue_name"],
    buckets=[0.1, 0.5, 1.0, 5.0, 10.0, 30.0, 60.0, 120.0, 300.0, 600.0, 1800.0],  # up to 30 min
)

rabbitmq_messages_in_flight = Gauge(
    "rabbitmq_messages_in_flight",
    "Number of messages currently being processed",
    ["queue_name"],
)

# Message Size Metrics
rabbitmq_message_size_bytes = Histogram(
    "rabbitmq_message_size_bytes",
    "Size of messages in bytes",
    ["queue_name"],
    buckets=[1024, 10240, 102400, 1048576, 10485760, 104857600],  # 1KB to 100MB
)

# ============================================================================
# Thread Pool Metrics
# ============================================================================

rabbitmq_worker_threads_active = Gauge(
    "rabbitmq_worker_threads_active",
    "Number of active worker threads processing messages",
    ["queue_name"],
)

rabbitmq_worker_threads_total = Gauge(
    "rabbitmq_worker_threads_total",
    "Total number of worker threads in the pool",
    ["queue_name"],
)

# ============================================================================
# ACK/NACK Metrics
# ============================================================================

rabbitmq_acks_total = Counter(
    "rabbitmq_acks_total",
    "Total number of message acknowledgements sent",
    ["queue_name", "status"],  # status: success|failed
)

rabbitmq_nacks_total = Counter(
    "rabbitmq_nacks_total",
    "Total number of message rejections sent",
    ["queue_name", "requeue"],  # requeue: true|false
)

# ============================================================================
# Consumer Health
# ============================================================================

consumer_health = Gauge(
    "rabbitmq_consumer_health",
    "Health status of RabbitMQ consumers (1=healthy, 0=unhealthy)",
    ["queue_name"],
)

topology_declarations_total = Counter(
    "rabbitmq_topology_declarations_total",
    "Total number of topology declarations (exchange/queue creation)",
    ["queue_name", "status"],  # status: success|failed
)

# ============================================================================
# Database Metrics
# ============================================================================

database_operations_total = Counter(
    "k8s_collector_database_operations_total",
    "Total database operations by type",
    ["operation", "table", "status"],  # operation: insert|update|delete, status: success|failed
)

database_operation_duration_seconds = Histogram(
    "k8s_collector_database_operation_duration_seconds",
    "Database operation duration",
    ["operation", "table"],
    buckets=[0.01, 0.05, 0.1, 0.5, 1.0, 5.0, 10.0],
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
    handler_processing_duration_seconds.labels(
        handler_type=handler_type,
        tenant_id=tenant_id,
        account_id=account_id,
        action=action,
    ).observe(duration_seconds)
    handler_processing_total.labels(
        handler_type=handler_type,
        tenant_id=tenant_id,
        account_id=account_id,
        action=action,
        status=status,
    ).inc()
    logger.debug(
        f"Metrics: {handler_type}/{action} for tenant={tenant_id}, account={account_id} "
        f"took {duration_seconds:.2f}s, status={status}"
    )


def record_handler_error(handler_type: str, tenant_id: str, account_id: str, error_type: str):
    """Record handler error"""
    handler_errors_total.labels(
        handler_type=handler_type,
        tenant_id=tenant_id,
        account_id=account_id,
        error_type=error_type,
    ).inc()


def record_discovery_operation(resource_type: str, tenant_id: str, account_id: str, operation: str):
    """Record discovery operation (create/update/delete)"""
    discovery_operations_total.labels(
        resource_type=resource_type,
        tenant_id=tenant_id,
        account_id=account_id,
        operation=operation,
    ).inc()


def record_event_operation(event_type: str, tenant_id: str, account_id: str, severity: str):
    """Record event operation"""
    event_operations_total.labels(
        event_type=event_type,
        tenant_id=tenant_id,
        account_id=account_id,
        severity=severity,
    ).inc()


def record_telemetry_update(tenant_id: str, account_id: str, integration_type: str):
    """Record telemetry update"""
    telemetry_updates_total.labels(
        tenant_id=tenant_id,
        account_id=account_id,
        integration_type=integration_type,
    ).inc()


def record_resource_processed(resource_type: str, tenant_id: str, account_id: str):
    """Record resource processing"""
    resources_processed_total.labels(
        resource_type=resource_type,
        tenant_id=tenant_id,
        account_id=account_id,
    ).inc()


def record_connection_established(queue_name: str, connection_type: str = "consumer"):
    """Record a new connection establishment"""
    rabbitmq_connection_total.labels(queue_name=queue_name, connection_type=connection_type).inc()
    rabbitmq_connection_status.labels(queue_name=queue_name).set(1)
    logger.debug(f"Metrics: Connection established for {queue_name}")


def record_connection_error(queue_name: str, error_type: str):
    """Record a connection error"""
    rabbitmq_connection_errors_total.labels(queue_name=queue_name, error_type=error_type).inc()
    rabbitmq_connection_status.labels(queue_name=queue_name).set(0)
    logger.debug(f"Metrics: Connection error for {queue_name}: {error_type}")


def record_reconnection(queue_name: str, success: bool):
    """Record a reconnection attempt"""
    rabbitmq_reconnections_total.labels(queue_name=queue_name, success=str(success).lower()).inc()
    if success:
        rabbitmq_connection_status.labels(queue_name=queue_name).set(1)
    logger.debug(f"Metrics: Reconnection {'succeeded' if success else 'failed'} for {queue_name}")


def record_message_received(queue_name: str, message_size_bytes: int = 0):
    """Record a message reception"""
    rabbitmq_messages_received_total.labels(queue_name=queue_name).inc()
    if message_size_bytes > 0:
        rabbitmq_message_size_bytes.labels(queue_name=queue_name).observe(message_size_bytes)
    logger.debug(f"Metrics: Message received from {queue_name} ({message_size_bytes} bytes)")


def record_message_processed(queue_name: str, duration_seconds: float):
    """Record successful message processing"""
    rabbitmq_messages_processed_total.labels(queue_name=queue_name).inc()
    rabbitmq_message_processing_duration_seconds.labels(queue_name=queue_name).observe(duration_seconds)
    logger.debug(f"Metrics: Message processed from {queue_name} in {duration_seconds:.2f}s")


def record_message_failed(queue_name: str, reason: str):
    """Record message processing failure"""
    rabbitmq_messages_failed_total.labels(queue_name=queue_name, reason=reason).inc()
    logger.debug(f"Metrics: Message failed from {queue_name}: {reason}")


def record_message_dlq(queue_name: str):
    """Record message sent to DLQ"""
    rabbitmq_messages_dlq_total.labels(queue_name=queue_name).inc()
    logger.debug(f"Metrics: Message sent to DLQ from {queue_name}")


def record_ack(queue_name: str, success: bool):
    """Record message acknowledgement"""
    status = "success" if success else "failed"
    rabbitmq_acks_total.labels(queue_name=queue_name, status=status).inc()


def record_nack(queue_name: str, requeue: bool):
    """Record message rejection"""
    rabbitmq_nacks_total.labels(queue_name=queue_name, requeue=str(requeue).lower()).inc()


def set_messages_in_flight(queue_name: str, count: int):
    """Set the number of messages currently being processed"""
    rabbitmq_messages_in_flight.labels(queue_name=queue_name).set(count)


def set_worker_threads(queue_name: str, active: int, total: int):
    """Set worker thread counts"""
    rabbitmq_worker_threads_active.labels(queue_name=queue_name).set(active)
    rabbitmq_worker_threads_total.labels(queue_name=queue_name).set(total)


def record_topology_declaration(queue_name: str, success: bool):
    """Record topology declaration attempt"""
    status = "success" if success else "failed"
    topology_declarations_total.labels(queue_name=queue_name, status=status).inc()


def set_consumer_health(queue_name: str, healthy: bool):
    """Set consumer health status"""
    consumer_health.labels(queue_name=queue_name).set(1 if healthy else 0)


def record_database_operation(operation: str, table: str, duration_seconds: float, success: bool):
    """Record database operation"""
    status = "success" if success else "failed"
    database_operations_total.labels(operation=operation, table=table, status=status).inc()
    database_operation_duration_seconds.labels(operation=operation, table=table).observe(duration_seconds)


class LabeledGaugeWrapper:
    """Wrapper around a labeled Prometheus Gauge for convenient inc/dec/get operations."""

    def __init__(self, gauge: Gauge, label_kwargs: Dict[str, str]):
        self._gauge = gauge.labels(**label_kwargs)

    def inc(self, amount: int = 1) -> None:
        self._gauge.inc(amount)

    def dec(self, amount: int = 1) -> None:
        self._gauge.dec(amount)

    def set(self, value: int) -> None:
        self._gauge.set(value)

    def get(self) -> float:
        return self._gauge._value.get()


_consumer_active_tasks_gauge = Gauge(
    "rabbitmq_consumer_active_tasks",
    "Number of tasks currently being processed by a specific consumer",
    ["queue_name"],
)

_consumer_active_tasks_gauges = {}
_consumer_active_tasks_gauges_lock = threading.Lock()


def get_consumer_active_tasks_gauge(queue_name: str) -> LabeledGaugeWrapper:
    """Get or create a Gauge for tracking active tasks for a specific consumer queue."""
    with _consumer_active_tasks_gauges_lock:
        if queue_name not in _consumer_active_tasks_gauges:
            _consumer_active_tasks_gauges[queue_name] = LabeledGaugeWrapper(
                _consumer_active_tasks_gauge, {"queue_name": queue_name}
            )
        return _consumer_active_tasks_gauges[queue_name]
