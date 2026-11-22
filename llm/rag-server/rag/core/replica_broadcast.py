"""Replica broadcast (issue #40807).

A short "this changed" message that every pod of every listening service
receives over Redis pub/sub, so each pod drops its own in-process copy. The
wire format is shared with services-server and llm-server; the one copy of it
is docs/fixtures/replica_message.json.

- A handler runs once per pod, so it may only drop that pod's own memory.
  Anything that must happen once per service is a RabbitMQ task instead.
- A message carries keys, never values. No keys means everything of that type.
- Every (re)subscribe runs each handler's on_all, because pub/sub does not
  redeliver messages sent while this pod was disconnected.
"""

import json
import logging
import os
import socket
import threading
import time
import uuid
from dataclasses import dataclass
from typing import Any, Callable, Dict, List, Optional

from utils.config import Config

logger = logging.getLogger(__name__)

CHANNEL = "nb_replica_broadcast"

# Change types this service sends or handles, and what keys hold for each.
# The list across services is kept in issue #40807.
CHANGE_RAG_COLLECTIONS = "rag_collections_changed"  # keys: collection names

# "<service>/<pod>#<process id>": the random part keeps it unique even when pods
# share a name, so skipping our own messages never skips a peer's.
_POD = os.environ.get("RAG_SERVER_NAME", "").strip() or socket.gethostname()
SENDER = f"rag-server/{_POD}#{uuid.uuid4().hex[:8]}"

# The subscriber polls in slices of this long and pings Redis at the same
# interval, so a connection dropped without a FIN/RST (NAT, LB, failover) shows
# up as a read timeout and is re-established, rather than blocking forever.
_POLL_SECONDS = 15.0


@dataclass(frozen=True)
class _Handler:
    on_keys: Callable[[List[str]], None]
    on_all: Callable[[], None]


_handlers: Dict[str, List[_Handler]] = {}
_handlers_lock = threading.Lock()
_publisher = None
_publisher_lock = threading.Lock()


def on_replica_broadcast(
    change_type: str, *, on_keys: Callable[[List[str]], None], on_all: Callable[[], None]
) -> Callable[[], None]:
    """Register handlers for one change type; returns a function that removes them.

    on_keys drops the entries for those keys; on_all drops everything of the type
    (also run after every reconnect). Both are required.
    """
    if on_keys is None or on_all is None:
        raise ValueError(f"replica broadcast: handler for {change_type} needs both on_keys and on_all")
    handler = _Handler(on_keys=on_keys, on_all=on_all)
    with _handlers_lock:
        _handlers.setdefault(change_type, []).append(handler)

    def unregister() -> None:
        with _handlers_lock:
            _handlers[change_type] = [h for h in _handlers.get(change_type, []) if h is not handler]

    return unregister


def _dispatch(change_type: str, keys: List[str]) -> bool:
    with _handlers_lock:
        handlers = list(_handlers.get(change_type, []))
    for handler in handlers:
        try:
            if keys:
                handler.on_keys(keys)
            else:
                handler.on_all()
        except Exception:
            logger.exception("replica broadcast: handler failed for %s", change_type)
    return bool(handlers)


def _resync_all() -> None:
    with _handlers_lock:
        types = list(_handlers.keys())
    for change_type in types:
        _dispatch(change_type, [])


def _redis_enabled() -> bool:
    return Config.cache_provider == "redis" and bool(Config.cache_redis_host)


def _new_redis_client(socket_timeout: Optional[float], health_check_interval: int = 0):
    import redis

    return redis.Redis(
        host=Config.cache_redis_host,
        port=Config.cache_redis_port,
        username=Config.cache_redis_username or None,
        password=Config.cache_redis_password or None,
        socket_connect_timeout=Config.cache_redis_timeout_seconds,
        socket_timeout=socket_timeout,
        socket_keepalive=True,
        health_check_interval=health_check_interval,
        decode_responses=True,
    )


def replica_broadcast(change_type: str, *keys: str) -> None:
    """Drop these keys on this pod at once, then tell every other pod.

    Call it after the write has completed. With no keys it is
    replica_broadcast_all. Best-effort: a failed publish is logged and never
    fails the caller; the collection writes that call it are not on a hot path,
    so the publish (bounded by the Redis timeout) runs inline.
    """
    global _publisher
    message: Dict[str, Any] = {"type": change_type, "sender": SENDER}
    if keys:
        message["keys"] = list(keys)
    _dispatch(change_type, list(keys))
    if not _redis_enabled():
        return
    try:
        with _publisher_lock:
            if _publisher is None:
                _publisher = _new_redis_client(Config.cache_redis_timeout_seconds)
            publisher = _publisher
        publisher.publish(CHANNEL, json.dumps(message))
        logger.info("replica broadcast: published type=%s keys=%d", change_type, len(keys))
    except Exception as e:
        logger.warning("replica broadcast: failed to publish %s: %s", change_type, e)


def replica_broadcast_all(change_type: str) -> None:
    """Drop everything of change_type on every pod."""
    replica_broadcast(change_type)


def _listen_forever() -> None:
    while True:
        client = None
        pubsub = None
        try:
            client = _new_redis_client(socket_timeout=_POLL_SECONDS * 2, health_check_interval=int(_POLL_SECONDS))
            pubsub = client.pubsub()
            pubsub.subscribe(CHANNEL)
            while True:
                # Returns None after the poll slice; each call also sends the
                # health-check PING once the interval has passed, and a dead
                # connection then fails the read and lands in the except below.
                msg = pubsub.get_message(timeout=_POLL_SECONDS)
                if msg is None:
                    continue
                kind = msg.get("type")
                if kind == "subscribe":
                    logger.info("replica broadcast: subscribed, resyncing caches")
                    _resync_all()
                elif kind == "message":
                    try:
                        message = json.loads(msg.get("data") or "")
                    except (TypeError, ValueError):
                        logger.warning("replica broadcast: ignoring malformed message")
                        continue
                    if not isinstance(message, dict) or not message.get("type"):
                        continue
                    # This pod already ran its handlers when it published.
                    if message.get("sender") == SENDER:
                        continue
                    keys = list(message.get("keys") or [])
                    if _dispatch(message["type"], keys):
                        logger.info(
                            "replica broadcast: received type=%s keys=%d sender=%s",
                            message["type"],
                            len(keys),
                            message.get("sender"),
                        )
        except Exception as e:
            logger.warning("replica broadcast: subscription lost, reconnecting: %s", e)
        finally:
            for resource in (pubsub, client):
                if resource is not None:
                    try:
                        resource.close()
                    except Exception:
                        pass
        time.sleep(1)


def start_replica_broadcast_subscriber() -> None:
    """Listen for other pods' messages for the life of the process."""
    if not _redis_enabled():
        logger.warning(
            "replica broadcast: OFF (CACHE_PROVIDER is not redis); changes apply only on the pod that made them"
        )
        return
    threading.Thread(target=_listen_forever, name="replica-broadcast-subscriber", daemon=True).start()
