"""
Redis client for distributed per-account locking in k8s-collector.

Prevents concurrent processing of discovery messages for the same cloud_account_id,
which causes PostgreSQL deadlocks on cloud_resourses INSERT ... ON CONFLICT.

Falls back to process-local threading.Lock if Redis is unavailable.
"""

import logging
import threading
import time
import uuid
from contextlib import contextmanager

import redis

logger = logging.getLogger(__name__)

_client = None
_client_lock = threading.Lock()
_fallback_locks: dict[str, threading.Lock] = {}
_fallback_registry_lock = threading.Lock()

LOCK_PREFIX = "k8s-collector:discovery-lock:"
LOCK_TTL = 600  # 10 min safety TTL (if process crashes)
LOCK_WAIT_TIMEOUT = 300  # 5 min max wait
LOCK_POLL_INTERVAL = 1  # 1 sec between retries

CLEANUP_TS_PREFIX = "k8s-collector:cleanup-ts:"
CLEANUP_MIN_INTERVAL = 60  # Skip cleanup if one ran within this many seconds

# Lua script: atomic release only if we own the lock (token matches)
RELEASE_SCRIPT = """
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
end
return 0
"""


def get_redis_client(host: str, port: int, username: str, password: str) -> redis.Redis | None:
    """Lazy singleton Redis client. Returns None if connection fails."""
    global _client
    if _client is not None:
        return _client
    with _client_lock:
        if _client is not None:
            return _client
        try:
            client = redis.Redis(
                host=host,
                port=port,
                username=username,
                password=password,
                socket_connect_timeout=5,
                socket_keepalive=True,
            )
            client.ping()
            _client = client
            logger.info("Redis connected: %s:%s", host, port)
            return _client
        except Exception:
            logger.warning("Redis unavailable, distributed locks disabled", exc_info=True)
            return None


@contextmanager
def acquire_discovery_lock(account_id: str, redis_config):
    """Acquire per-account lock. Blocks until available or timeout.

    Falls back to process-local threading.Lock if Redis is unavailable.
    """
    client = get_redis_client(
        redis_config.REDIS_SERVER_HOST,
        redis_config.REDIS_SERVER_PORT,
        redis_config.REDIS_USER_NAME,
        redis_config.REDIS_USER_PASSWORD,
    )
    if client is None:
        yield from _fallback_lock(account_id)
        return

    key = LOCK_PREFIX + account_id
    token = str(uuid.uuid4())
    deadline = time.monotonic() + LOCK_WAIT_TIMEOUT

    while True:
        acquired = client.set(key, token, nx=True, ex=LOCK_TTL)
        if acquired:
            logger.debug("Acquired discovery lock for account %s", account_id)
            try:
                yield
            finally:
                try:
                    client.eval(RELEASE_SCRIPT, 1, key, token)
                except Exception:
                    logger.warning("Failed to release discovery lock for account %s", account_id, exc_info=True)
            return

        if time.monotonic() >= deadline:
            raise TimeoutError(f"Could not acquire discovery lock for account {account_id} within {LOCK_WAIT_TIMEOUT}s")
        time.sleep(LOCK_POLL_INTERVAL)


def should_skip_cleanup(account_id: str, resource_type: str, redis_config) -> bool:
    """Return True if cleanup for this account+type ran recently and should be skipped.

    Prevents redundant cleanup runs when the queue is backlogged with multiple
    "last batch" messages for the same account. The upsert path still runs —
    only the expensive cleanup (handle_active_resources_deletion) is skipped.
    """
    client = get_redis_client(
        redis_config.REDIS_SERVER_HOST,
        redis_config.REDIS_SERVER_PORT,
        redis_config.REDIS_USER_NAME,
        redis_config.REDIS_USER_PASSWORD,
    )
    if client is None:
        return False  # Can't check — always run cleanup

    key = f"{CLEANUP_TS_PREFIX}{account_id}:{resource_type}"
    try:
        last_ts = client.get(key)
        if last_ts is not None:
            elapsed = time.time() - float(last_ts)
            if elapsed < CLEANUP_MIN_INTERVAL:
                logger.info(
                    "Skipping cleanup for %s/%s — last ran %.0fs ago (min interval %ds)",
                    account_id,
                    resource_type,
                    elapsed,
                    CLEANUP_MIN_INTERVAL,
                )
                return True
    except Exception:
        logger.debug("Failed to check cleanup timestamp, will run cleanup", exc_info=True)
    return False


def record_cleanup_done(account_id: str, resource_type: str, redis_config) -> None:
    """Record that cleanup completed for this account+type."""
    client = get_redis_client(
        redis_config.REDIS_SERVER_HOST,
        redis_config.REDIS_SERVER_PORT,
        redis_config.REDIS_USER_NAME,
        redis_config.REDIS_USER_PASSWORD,
    )
    if client is None:
        return

    key = f"{CLEANUP_TS_PREFIX}{account_id}:{resource_type}"
    try:
        client.set(key, str(time.time()), ex=CLEANUP_MIN_INTERVAL * 2)
    except Exception:
        logger.debug("Failed to record cleanup timestamp", exc_info=True)


def _fallback_lock(account_id: str):
    """Process-local fallback when Redis is unavailable."""
    lock = _fallback_locks.get(account_id)
    if lock is None:
        with _fallback_registry_lock:
            if account_id not in _fallback_locks:
                _fallback_locks[account_id] = threading.Lock()
            lock = _fallback_locks[account_id]

    acquired = lock.acquire(timeout=LOCK_WAIT_TIMEOUT)
    if not acquired:
        raise TimeoutError(f"Could not acquire fallback lock for account {account_id} within {LOCK_WAIT_TIMEOUT}s")
    try:
        yield
    finally:
        lock.release()
