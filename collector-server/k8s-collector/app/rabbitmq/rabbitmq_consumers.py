"""
RabbitMQ consumer registration module.
Provides explicit consumer setup instead of side-effect imports.
"""

import json
import logging
from typing import Any

from config import Configs
from handlers.account_status import is_account_disabled
from handlers.discovery_handler import run_async_discovery_handler
from handlers.event_handler import run_event_handler_async
from handlers.spend_handler import process_spend
from metrics import prometheus_metrics
from rabbitmq.rabbitmq_client import consume_message

logger = logging.getLogger(__name__)


def _is_from_disabled_account(data: Any, queue_name: str) -> bool:
    """True when the message belongs to a cloud account that is disabled.

    Dropping happens here, at the single point every queue's payload carries
    `cloud_account_id`, rather than inside each handler -- a disabled account
    should have nothing ingested at all, not merely be skipped per resource
    type. Returning True makes the callback a no-op, so the client acks and
    discards the message; it is deliberately not rejected to the DLQ, since a
    disabled account publishes continuously and would fill the DLQ forever.
    """
    if not Configs.DROP_DISABLED_ACCOUNT_MESSAGES:
        return False

    if isinstance(data, (str, bytes)):
        try:
            data = json.loads(data)
        except ValueError:
            return False
    if not isinstance(data, dict):
        return False

    cloud_account_id = data.get("cloud_account_id")
    if not cloud_account_id or not is_account_disabled(cloud_account_id):
        return False

    logger.info(
        "Dropping message from %s: cloud account %s (tenant %s) is disabled",
        queue_name,
        cloud_account_id,
        data.get("tenant"),
    )
    prometheus_metrics.record_message_dropped_disabled(queue_name, cloud_account_id)
    return True


def _discovery_callback(data: Any) -> None:
    """Wrapper for discovery handler"""
    if _is_from_disabled_account(data, Configs.RABBIT_MQ_DISCOVERY_QUEUE):
        return
    run_async_discovery_handler(data)


def _event_callback(data: Any) -> None:
    """Wrapper for event handler"""
    if _is_from_disabled_account(data, Configs.RABBIT_MQ_EVENT_QUEUE):
        return
    run_event_handler_async(data)


def _spend_callback(data: Any) -> None:
    """Wrapper for spend handler"""
    if not isinstance(data, dict):
        logger.error("Invalid spend data format: %s", type(data))
        return

    tenant = data.get("tenant")
    cloud_account_id = data.get("cloud_account_id")

    if not tenant or not cloud_account_id:
        logger.error("Missing tenant or cloud_account_id in spend data: %s", data)
        return

    if _is_from_disabled_account(data, Configs.RABBIT_MQ_SPEND_QUEUE):
        return

    process_spend(data, tenant, cloud_account_id)


def register_all_consumers():
    """Register all RabbitMQ consumers explicitly."""
    logger.info("Registering RabbitMQ consumers...")

    max_workers = Configs.K8S_COLLECTOR_CONSUMER_MAX_WORKERS
    logger.info("Using max_workers=%d for consumers", max_workers)

    # Discovery consumer
    consume_message(
        Configs.RABBIT_MQ_DISCOVERY_EXCHANGE,
        Configs.RABBIT_MQ_DISCOVERY_QUEUE,
        Configs.RABBIT_MQ_DISCOVERY_QUEUE,
        _discovery_callback,
        max_workers=max_workers,
    )
    logger.info("Registered discovery consumer")

    # Event consumer
    consume_message(
        Configs.RABBIT_MQ_EVENT_EXCHANGE,
        Configs.RABBIT_MQ_EVENT_QUEUE,
        Configs.RABBIT_MQ_EVENT_QUEUE,
        _event_callback,
        max_workers=max_workers,
    )
    logger.info("Registered event consumer")

    # Spend consumer
    consume_message(
        Configs.RABBIT_MQ_EVENT_EXCHANGE,
        Configs.RABBIT_MQ_SPEND_QUEUE,
        Configs.RABBIT_MQ_SPEND_QUEUE,
        _spend_callback,
        max_workers=max_workers,
    )
    logger.info("Registered spend consumer")

    logger.info("All RabbitMQ consumers registered successfully")
