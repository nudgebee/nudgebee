"""Cache backend: Redis when it is reachable, process memory when it is not.

rag-server has only ever cached in process memory. This adds Redis as an
option so cached state can outlive a restart and, once rag-server runs more
than one replica, be shared between them.

Two deliberate choices:

* **It falls back.** llm-server picks a provider at boot and keeps it, so a
  Redis outage there turns every read into a miss for the lifetime of the pod.
  Here a backend that fails repeatedly is swapped for the in-memory one, which
  is the behaviour `ticket-server/services/cache` already has.

* **No credentials.** Only non-secret, serialisable state belongs here. The
  integration caches in `rag/core/utils/db_query.py` hold decrypted credentials
  and stay in process memory, matching llm-server's `llmIntegrationsCache` and
  api-server's `integrationByTypeCache`.
"""

import copy
import json
import logging
import threading
import time
import uuid
from typing import Any, Dict, Optional, Protocol

from utils.config import Config

logger = logging.getLogger(__name__)

# Consecutive backend errors before giving up on Redis for this process.
# One blip shouldn't cost the shared cache; a sustained outage shouldn't cost
# every request a connection timeout.
_FAILURE_THRESHOLD = 3

# Keys deleted per round trip when clearing a prefix.
_SCAN_BATCH = 500


class CacheBackend(Protocol):
    """Key-value store with a TTL.

    Implementations must never raise: a backend problem is reported as a miss
    or a False return, so callers never have to handle cache errors.
    """

    name: str

    def get(self, key: str) -> Optional[Any]:
        raise NotImplementedError

    def set(self, key: str, value: Any, ttl_seconds: Optional[int] = None) -> bool:
        raise NotImplementedError

    def set_many(self, items: Dict[str, Any], ttl_seconds: Optional[int] = None, local_only: bool = False) -> bool:
        """local_only skips the shared tier, for data that came from it."""
        raise NotImplementedError

    def delete(self, key: str) -> bool:
        raise NotImplementedError

    def clear(self, prefix: str = "") -> bool:
        raise NotImplementedError

    def healthy(self) -> bool:
        raise NotImplementedError

    def try_lock(self, key: str, ttl_seconds: int) -> Optional[str]:
        """Acquire a lock, returning a token, or None if someone else holds it."""
        raise NotImplementedError

    def unlock(self, key: str, token: str) -> None:
        """Release a lock, but only if we still hold it."""
        raise NotImplementedError


class InMemoryBackend:
    """Process-local dict with per-key expiry. Always available."""

    name = "in_memory"

    def __init__(self) -> None:
        self._data: Dict[str, Any] = {}
        self._expiry: Dict[str, float] = {}
        self._locks: Dict[str, float] = {}
        self._lock_tokens: Dict[str, str] = {}
        self._lock = threading.RLock()

    def get(self, key: str) -> Optional[Any]:
        with self._lock:
            expires_at = self._expiry.get(key)
            if expires_at is not None and time.time() > expires_at:
                self._data.pop(key, None)
                self._expiry.pop(key, None)
                return None
            # Copy on the way out. The Redis backend deserialises JSON, so it
            # hands back a fresh object every time; without this the two
            # backends differ on whether a caller can mutate the cache by
            # mutating what it was given, and the whole point is that they are
            # interchangeable.
            return copy.deepcopy(self._data.get(key))

    def set(self, key: str, value: Any, ttl_seconds: Optional[int] = None) -> bool:
        with self._lock:
            # Copy on the way in too, so a caller mutating the object it just
            # stored does not reach back into the cache.
            self._data[key] = copy.deepcopy(value)
            if ttl_seconds:
                self._expiry[key] = time.time() + ttl_seconds
            else:
                self._expiry.pop(key, None)
            return True

    def set_many(self, items: Dict[str, Any], ttl_seconds: Optional[int] = None, local_only: bool = False) -> bool:
        with self._lock:
            for k, v in items.items():
                self.set(k, v, ttl_seconds)
            return True

    def delete(self, key: str) -> bool:
        with self._lock:
            self._data.pop(key, None)
            self._expiry.pop(key, None)
            return True

    def clear(self, prefix: str = "") -> bool:
        with self._lock:
            for k in [k for k in self._data if k.startswith(prefix)]:
                self._data.pop(k, None)
                self._expiry.pop(k, None)
            return True

    def healthy(self) -> bool:
        return True

    def try_lock(self, key: str, ttl_seconds: int) -> Optional[str]:
        now = time.time()
        with self._lock:
            held_until = self._locks.get(key)
            if held_until is not None and held_until > now:
                return None
            token = uuid.uuid4().hex
            self._locks[key] = now + ttl_seconds
            self._lock_tokens[key] = token
            return token

    def unlock(self, key: str, token: str) -> None:
        with self._lock:
            if self._lock_tokens.get(key) == token:
                self._locks.pop(key, None)
                self._lock_tokens.pop(key, None)


class RedisBackend:
    """Redis-backed store. Values are JSON, so only JSON-able state fits.

    Every operation swallows its errors and reports failure instead, so a
    caller never sees a Redis problem as an exception -- it sees a miss, and
    the selector below eventually swaps this backend out.
    """

    name = "redis"

    def __init__(self, client: Any, key_prefix: str = "rag:") -> None:
        self._client = client
        self._prefix = key_prefix
        self._failures = 0
        self._retired_at = 0.0
        self._lock = threading.Lock()

    def _k(self, key: str) -> str:
        return self._prefix + key

    def _record(self, ok: bool) -> None:
        with self._lock:
            if ok:
                if self._failures >= _FAILURE_THRESHOLD:
                    logger.info("cache: redis answered again, resuming shared cache")
                self._failures = 0
                self._retired_at = 0.0
                return
            failures = self._failures + 1
            if failures >= _FAILURE_THRESHOLD:
                # Stamped BEFORE _failures is published, so a lock-free reader
                # can never see the count past the threshold while the clock
                # still reads 0.0 -- that pairing measures against the epoch
                # and waves a probe through mid-outage.
                #
                # Restamped on every failure, so a probe that fails starts the
                # wait again rather than letting the next call straight through.
                # monotonic, not time(): an NTP step backwards would otherwise
                # push the next probe out by however far the clock moved.
                self._retired_at = time.monotonic()
            if failures == _FAILURE_THRESHOLD:
                logger.error(
                    "cache: redis failed %d times, serving from in-memory only until it answers again",
                    _FAILURE_THRESHOLD,
                )
            self._failures = failures

    def get(self, key: str) -> Optional[Any]:
        value, _ = self.get_with_ttl(key)
        return value

    def get_with_ttl(self, key: str) -> "tuple[Optional[Any], Optional[int]]":
        """Value plus its remaining TTL, in one round trip.

        The TTL matters because the tiered backend copies what it reads here
        into its local tier; without it, a key that expires in Redis would
        live in memory until the process restarted.
        """
        try:
            pipe = self._client.pipeline(transaction=False)
            pipe.get(self._k(key))
            pipe.ttl(self._k(key))
            raw, ttl = pipe.execute()
            self._record(True)
        except Exception as e:
            self._record(False)
            logger.warning("cache: redis get failed for %s: %s", key, e)
            return None, None
        if raw is None:
            return None, None
        # Decoding happens outside the block above on purpose: a corrupt
        # value is a data problem, and counting it against Redis health
        # would retire a perfectly healthy server after three of them.
        try:
            value = json.loads(raw)
        except (TypeError, ValueError) as e:
            logger.warning("cache: discarding undecodable value for %s: %s", key, e)
            return None, None
        # TTL: -1 means the key has no expiry, -2 means it is gone.
        remaining = ttl if isinstance(ttl, int) and ttl > 0 else None
        return value, remaining

    def set(self, key: str, value: Any, ttl_seconds: Optional[int] = None) -> bool:
        try:
            payload = json.dumps(value)
        except (TypeError, ValueError) as e:
            logger.warning("cache: value for %s is not serialisable: %s", key, e)
            return False
        try:
            self._client.set(self._k(key), payload, ex=ttl_seconds or None)
            self._record(True)
            return True
        except Exception as e:
            self._record(False)
            logger.warning("cache: redis set failed for %s: %s", key, e)
            return False

    def set_many(self, items: Dict[str, Any], ttl_seconds: Optional[int] = None, local_only: bool = False) -> bool:
        """One round trip for the whole batch.

        The collection-metadata refresh writes a few hundred entries at once;
        as individual SETs that is a few hundred round trips, which dominated
        the refresh when Redis was not local.
        """
        if not items or local_only:
            return True
        try:
            payloads = {k: json.dumps(v) for k, v in items.items()}
        except (TypeError, ValueError) as e:
            logger.warning("cache: batch contains a non-serialisable value: %s", e)
            return False
        try:
            pipe = self._client.pipeline(transaction=False)
            for k, payload in payloads.items():
                pipe.set(self._k(k), payload, ex=ttl_seconds or None)
            pipe.execute()
            self._record(True)
            return True
        except Exception as e:
            self._record(False)
            logger.warning("cache: redis set_many failed for %d keys: %s", len(items), e)
            return False

    def delete(self, key: str) -> bool:
        try:
            self._client.delete(self._k(key))
            self._record(True)
            return True
        except Exception as e:
            self._record(False)
            logger.warning("cache: redis delete failed for %s: %s", key, e)
            return False

    def clear(self, prefix: str = "") -> bool:
        try:
            pattern = self._k(prefix) + "*"
            # Delete as we scan. Collecting every key first and issuing one
            # DEL holds the whole set in memory and blocks Redis for the
            # length of the command, which grows with the keyspace.
            batch = []
            for key in self._client.scan_iter(match=pattern, count=_SCAN_BATCH):
                batch.append(key)
                if len(batch) >= _SCAN_BATCH:
                    self._client.delete(*batch)
                    batch = []
            if batch:
                self._client.delete(*batch)
            self._record(True)
            return True
        except Exception as e:
            self._record(False)
            logger.warning("cache: redis clear failed for %s: %s", prefix, e)
            return False

    def healthy(self) -> bool:
        """True while usable, and again once the retry interval has passed.

        Reporting healthy after the wait is how a retired backend gets probed
        at all: callers skip an unhealthy Redis entirely, so nothing would
        ever reach it to prove it had recovered.

        Deliberately free of side effects -- it is also how the selector and
        callers *test* the backend, so consuming the window here would mean
        the probe never reaches Redis. _record restamps instead, which only
        runs when a real call has actually been made.

        Lock-free, because this sits on every write and every local miss.
        Both reads are atomic on their own, and _record publishes _retired_at
        before the _failures that makes it readable, so the one pairing that
        would matter -- a count past the threshold against a clock still at
        0.0 -- cannot be observed.
        """
        if self._failures < _FAILURE_THRESHOLD:
            return True
        return time.monotonic() - self._retired_at >= Config.cache_redis_retry_after_seconds

    def try_lock(self, key: str, ttl_seconds: int) -> Optional[str]:
        token = uuid.uuid4().hex
        try:
            # NX+PX is the whole lock: expiry means a holder that dies cannot
            # pin everyone else, and the token makes release safe.
            acquired = self._client.set(self._k(key), token, nx=True, ex=ttl_seconds)
            self._record(True)
            return token if acquired else None
        except Exception as e:
            self._record(False)
            logger.warning("cache: redis lock failed for %s: %s", key, e)
            return None

    def unlock(self, key: str, token: str) -> None:
        try:
            # Only delete if we still hold it: a lock that expired and was
            # retaken by someone else must not be released by the old holder.
            self._client.eval(
                "if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('del', KEYS[1]) else return 0 end",
                1,
                self._k(key),
                token,
            )
            self._record(True)
        except Exception as e:
            self._record(False)
            logger.warning("cache: redis unlock failed for %s: %s", key, e)


class TieredBackend:
    """Redis in front, process memory behind, written to together.

    Writes go to both tiers. Reads take the local tier first and fall through
    to Redis only on a miss, which is what makes a cold process start warm;
    see get() for why that order and not the reverse.

    Writing both is the point. A Redis-only cache that loses Redis mid-flight
    does not degrade, it empties: callers that treat a miss as "does not
    exist" start returning wrong answers rather than slow ones. Keeping the
    local tier warm costs exactly what the cache cost before Redis existed.
    """

    name = "redis+in_memory"

    def __init__(self, primary: CacheBackend, fallback: "InMemoryBackend") -> None:
        self._primary = primary
        self._fallback = fallback

    @property
    def primary_healthy(self) -> bool:
        return self._primary.healthy()

    def get(self, key: str) -> Optional[Any]:
        """Local first, shared on a miss -- an ordinary L1/L2 read.

        Reading Redis first would put a network round trip on every lookup,
        and metadata is read per document during processing. Because writes
        go through both tiers, the local copy is the same data, so the hop
        buys nothing once this process has seen the key.

        The cost is that another replica's invalidate() is not observed until
        this one refreshes. That is exactly today's behaviour with no shared
        cache at all, so it is not a regression -- and a cold process still
        reads the shared copy, which is what warm starts depend on.
        """
        value = self._fallback.get(key)
        if value is not None:
            return value
        if self._primary.healthy():
            # Take the remaining TTL with the value. Warming L1 without it
            # would keep a key that expires in Redis alive in memory until
            # the process restarted -- so this process would stop seeing
            # updates that every other replica picks up.
            getter = getattr(self._primary, "get_with_ttl", None)
            if getter is not None:
                value, remaining_ttl = getter(key)
            else:
                value, remaining_ttl = self._primary.get(key), None
            if value is not None:
                self._fallback.set(key, value, ttl_seconds=remaining_ttl)
                return value
        return None

    def set(self, key: str, value: Any, ttl_seconds: Optional[int] = None) -> bool:
        local = self._fallback.set(key, value, ttl_seconds)
        shared = self._primary.set(key, value, ttl_seconds) if self._primary.healthy() else False
        # Local success is enough: the caller's data is cached either way.
        return local or shared

    def set_many(self, items: Dict[str, Any], ttl_seconds: Optional[int] = None, local_only: bool = False) -> bool:
        local = self._fallback.set_many(items, ttl_seconds)
        if local_only:
            return local
        shared = self._primary.set_many(items, ttl_seconds) if self._primary.healthy() else False
        return local or shared

    def delete(self, key: str) -> bool:
        local = self._fallback.delete(key)
        shared = self._primary.delete(key) if self._primary.healthy() else False
        return local or shared

    def clear(self, prefix: str = "") -> bool:
        local = self._fallback.clear(prefix)
        shared = self._primary.clear(prefix) if self._primary.healthy() else False
        return local or shared

    def healthy(self) -> bool:
        return True

    def try_lock(self, key: str, ttl_seconds: int) -> Optional[str]:
        # The lock must be the shared one to be worth anything across pods;
        # with Redis gone, the local lock still serialises this process.
        if self._primary.healthy():
            return self._primary.try_lock(key, ttl_seconds)
        return self._fallback.try_lock(key, ttl_seconds)

    def unlock(self, key: str, token: str) -> None:
        if self._primary.healthy():
            self._primary.unlock(key, token)
        self._fallback.unlock(key, token)


_backend: Optional[CacheBackend] = None
_fallback: Optional[InMemoryBackend] = None
_backend_lock = threading.Lock()


def _build_redis_backend() -> Optional[RedisBackend]:
    """Connect and PING. Returns None if Redis is not usable right now."""
    try:
        import redis
    except ImportError:
        logger.warning("cache: CACHE_PROVIDER=redis but the redis package is not installed")
        return None

    if not Config.cache_redis_host:
        # Distinguish misconfiguration from an outage. Without this the only
        # signal is "Error 61 connecting to :6379", which reads as a down
        # Redis rather than a missing setting. Logged at ERROR so it is
        # visible, but not fatal: this service treats the shared cache as
        # optional, and refusing to boot would turn a cache problem into an
        # outage.
        logger.error(
            "cache: CACHE_PROVIDER is 'redis' but REDIS_SERVER_HOST is empty; "
            "serving from in-memory only, with no sharing between replicas"
        )
        return None

    try:
        client = redis.Redis(
            host=Config.cache_redis_host,
            port=Config.cache_redis_port,
            username=Config.cache_redis_username or None,
            password=Config.cache_redis_password or None,
            socket_connect_timeout=Config.cache_redis_timeout_seconds,
            socket_timeout=Config.cache_redis_timeout_seconds,
            decode_responses=True,
        )
        client.ping()
    except Exception as e:
        # Host deliberately not logged: it comes from the shared secret.
        logger.warning("cache: redis unreachable, using in-memory instead: %s", e)
        return None

    logger.info("cache: using redis backend")
    return RedisBackend(client)


def get_cache_backend() -> CacheBackend:
    """Return the active backend, degrading to in-memory when Redis misbehaves."""
    global _backend, _fallback

    # Fast path, no lock. Metadata is read per document during processing, so
    # every one of those reads would otherwise contend on this. Reading a
    # module global is atomic, and once the backend exists it never changes:
    # a retired Redis is handled inside TieredBackend, not by swapping the
    # backend out, so there is no transition left for this function to catch.
    backend = _backend
    if backend is not None:
        return backend

    with _backend_lock:
        if _fallback is None:
            _fallback = InMemoryBackend()

        if _backend is None:
            if Config.cache_provider == "redis":
                redis_backend = _build_redis_backend()
                _backend = TieredBackend(redis_backend, _fallback) if redis_backend else _fallback
            else:
                logger.info("cache: using in-memory backend")
                _backend = _fallback

        # A retired Redis is NOT swapped out for the bare fallback here.
        # TieredBackend already skips an unhealthy primary on every path, so
        # swapping would only make the retirement permanent: the RedisBackend
        # would be unreachable and could never be probed again. Crossing the
        # threshold is logged by RedisBackend._record, which is where it is
        # actually known.
        return _backend


def reset_cache_backend() -> None:
    """Drop the cached selection. For tests and for re-reading config."""
    global _backend, _fallback
    with _backend_lock:
        _backend = None
        _fallback = None
