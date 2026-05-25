import logging
import threading
import uuid
from datetime import datetime, timezone

from cachetools import TTLCache

from config import Configs
from rabbitmq.rabbitmq_client import publish_message

logger = logging.getLogger(__name__)

# 2 hours in milliseconds, matching the api-server publisher TTL
KG_UPDATE_MESSAGE_TTL_MS = 2 * 60 * 60 * 1000

# Cache TTL: publish at most once per hour per tenant_id
KG_UPDATE_CACHE_TTL_SECONDS = 60 * 60

_kg_update_cache: TTLCache = TTLCache(maxsize=10_000, ttl=KG_UPDATE_CACHE_TTL_SECONDS)
_kg_update_cache_lock = threading.Lock()


def publish_kg_update(tenant_id: str) -> None:
    """Publish a KG update message for a tenant.

    Each tenant_id is published at most once per hour (deduplicated via in-memory cache).
    Failures are logged but never raised -- KG updates are best-effort
    and must not break the discovery pipeline.
    """
    now = datetime.now(timezone.utc)

    with _kg_update_cache_lock:
        last_published = _kg_update_cache.get(tenant_id)
        if last_published is not None:
            elapsed = (now - last_published).total_seconds()
            logger.debug(
                "kg_update: skipping duplicate publish for tenant %s (within 1h cache TTL, last=%.0fs ago)",
                tenant_id,
                elapsed,
            )
            return
        _kg_update_cache[tenant_id] = now

    correlation_id = str(uuid.uuid4())
    message = {
        "tenant_id": tenant_id,
        "source": "k8s-collector",
        "requested_at": now.isoformat(),
        "correlation_id": correlation_id,
    }

    try:
        publish_message(
            exchange_name=Configs.RABBIT_MQ_KG_UPDATE_EXCHANGE,
            routing_key=Configs.RABBIT_MQ_KG_UPDATE_QUEUE,
            message=message,
            exchange_type="direct",
            message_ttl=KG_UPDATE_MESSAGE_TTL_MS,
        )
        logger.info(
            "kg_update: published KG update message for tenant %s (correlation_id=%s)",
            tenant_id,
            correlation_id,
        )
    except Exception:
        # Evict from cache so a retry is allowed on next call.
        with _kg_update_cache_lock:
            _kg_update_cache.pop(tenant_id, None)
        logger.exception(
            "kg_update: failed to publish KG update message for tenant %s",
            tenant_id,
        )
