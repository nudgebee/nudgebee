"""
RabbitMQ consumer registration module.
Provides explicit consumer setup instead of side-effect imports.
"""

import logging
from typing import Any

from config import Configs
from handlers.discovery_handler import run_async_discovery_handler
from handlers.event_handler import run_event_handler_async
from handlers.spend_handler import process_spend
from rabbitmq.rabbitmq_client import consume_message

logger = logging.getLogger(__name__)


def _discovery_callback(data: Any) -> None:
    """Wrapper for discovery handler"""
    run_async_discovery_handler(data)


def _event_callback(data: Any) -> None:
    """Wrapper for event handler"""
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
