import asyncio
import contextvars
import functools
import os
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor

import redis
import json
import logging
import uuid

from notifications_server.configs import settings
from notifications_server.models.models import MessagingPlatform

LOG = logging.getLogger(__name__)

# Bounds the WATCH/retry loop in update_event_entry against another writer
# repeatedly touching the same key — a handful of attempts is enough to ride
# out real contention without risking a busy-retry loop under sustained load.
_UPDATE_EVENT_ENTRY_MAX_RETRIES = 5

# After a connection error or timeout ends a Redis operation, Redis is skipped
# for this long: reads miss, writes are dropped, claims fail open.
COOL_DOWN_SECONDS = 30

# Cache.aio runs on its own threads, so a slow Redis can't use up the default
# executor that asyncio's DNS lookups and the other to_thread calls share.
_EXECUTOR = ThreadPoolExecutor(max_workers=16, thread_name_prefix="redis-cache")

# Set under pytest: a sync Cache call on a running event loop raises instead of
# logging a warning.
FORBID_EVENT_LOOP_CALLS_ENV = "NOTIFICATIONS_FORBID_EVENT_LOOP_REDIS"


def _teams_analysis_key(event_id):
    return f"event_analysis_poll:ms_teams:{event_id}"


class RedisCoolingDown(redis.exceptions.ConnectionError):
    """Raised instead of reaching Redis while it is being skipped."""


def _is_outage(error):
    return isinstance(error, (redis.exceptions.ConnectionError, redis.exceptions.TimeoutError)) and not isinstance(
        error, RedisCoolingDown
    )


class _CoolDown:
    """Whether Redis is being skipped. One per process: redis-py copies the Retry
    for every connection, so this can't live on it."""

    def __init__(self):
        self._lock = threading.Lock()
        self._until = 0.0
        self.trips = 0

    def skipping(self):
        return time.monotonic() < self._until

    def admit(self):
        """False while skipping. When the cool-down ends, the first caller is let
        through to probe Redis and the cool-down restarts for everyone else, until
        the probe succeeds (close) or fails (trip)."""
        if not self._until:
            return True
        with self._lock:
            if not self._until:
                # A probe closed it while we waited for the lock.
                return True
            now = time.monotonic()
            if now < self._until:
                return False
            self._until = now + COOL_DOWN_SECONDS
            return True

    def trip(self, error):
        with self._lock:
            was_open = not self._until
            self._until = time.monotonic() + COOL_DOWN_SECONDS
            self.trips += 1
        if was_open:
            LOG.warning(f"Redis is unreachable; skipping it for {COOL_DOWN_SECONDS}s. {error}")

    def close(self):
        if not self._until:
            return
        with self._lock:
            was_skipping = bool(self._until)
            self._until = 0.0
        if was_skipping:
            LOG.info("Redis is reachable again")

    def reset(self):
        with self._lock:
            self._until = 0.0
            self.trips = 0


_cool_down = _CoolDown()
_retry_state = threading.local()


class _CoolDownRetry(redis.retry.Retry):
    """redis-py runs every connect, command, health check and pipeline through
    call_with_retry, often one inside another. The outermost call is refused while
    Redis is being skipped. A connection error or timeout that ends a call at any
    depth starts the cool-down, and an operation already under way stops retrying
    once it has started."""

    def call_with_retry(self, do, fail, is_retryable=None, with_failure_count=False):
        depth = getattr(_retry_state, "depth", 0)
        if depth == 0:
            if not _cool_down.admit():
                raise RedisCoolingDown("Skipping Redis while it is unreachable")
            _retry_state.trips_at_start = _cool_down.trips
        trips_at_start = _retry_state.trips_at_start

        def attempt():
            if _cool_down.trips != trips_at_start:
                raise RedisCoolingDown("Redis became unreachable during this operation")
            return do()

        def on_failure(error, *fail_args):
            try:
                return fail(error, *fail_args)
            except Exception:
                # While a key is WATCHed, redis-py turns a lost connection into a
                # WatchError here, which callers retry; it still ends this call.
                if _is_outage(error):
                    _cool_down.trip(error)
                raise

        def retryable(error):
            if isinstance(error, RedisCoolingDown):
                return False
            return is_retryable(error) if is_retryable else True

        _retry_state.depth = depth + 1
        try:
            result = super().call_with_retry(attempt, on_failure, retryable, with_failure_count)
        except Exception as e:
            if _is_outage(e):
                _cool_down.trip(e)
            raise
        finally:
            _retry_state.depth = depth
        if depth == 0:
            _cool_down.close()
        return result


async def run_in_cache_thread(fn, *args, **kwargs):
    """Run a blocking Redis call on the cache's threads and await its result."""
    loop = asyncio.get_running_loop()
    call = functools.partial(contextvars.copy_context().run, fn, *args, **kwargs)
    return await loop.run_in_executor(_EXECUTOR, call)


class AsyncCache:
    """Cache.aio: every Cache method, awaitable, running off the event loop."""

    def __init__(self, cache):
        self._cache = cache

    def __getattr__(self, name):
        method = getattr(self._cache, name)

        async def call(*args, **kwargs):
            return await run_in_cache_thread(method, *args, **kwargs)

        return call


_warned_loop_sites = set()


def _check_not_on_event_loop():
    try:
        asyncio.get_running_loop()
    except RuntimeError:
        return
    message = "Sync Redis call on a running event loop; use `await cache.aio.<method>()`"
    if os.environ.get(FORBID_EVENT_LOOP_CALLS_ENV):
        raise RuntimeError(message)
    # Frames: this function, _ensure_connection, the Cache method, its caller.
    caller = sys._getframe(3)
    site = (caller.f_code.co_filename, caller.f_lineno)
    if site not in _warned_loop_sites:
        _warned_loop_sites.add(site)
        LOG.warning(f"{message} (at {site[0]}:{site[1]})")


class Cache:
    _redis_client = None

    def __init__(self):
        if settings.redis.is_enabled:
            if not Cache._redis_client:
                Cache._redis_client = self._create_redis_client()
            self.redis_client = Cache._redis_client
        else:
            self.redis_client = None

    @property
    def aio(self):
        return AsyncCache(self)

    @staticmethod
    def _create_redis_client():
        """Create a new Redis client with connection pooling and retry logic. The
        client is kept even when the first ping fails: it connects per command, so
        it starts working once Redis is reachable."""
        try:
            client = redis.Redis(
                host=settings.redis.host,
                port=settings.redis.port,
                username=settings.redis.username,
                password=settings.redis.password,
                decode_responses=True,
                socket_keepalive=True,
                socket_keepalive_options={},
                health_check_interval=30,
                retry_on_timeout=True,
                retry_on_error=[redis.exceptions.ConnectionError, redis.exceptions.TimeoutError],
                socket_connect_timeout=5,
                socket_timeout=5,
                retry=_CoolDownRetry(redis.backoff.ExponentialBackoff(), 3),
            )
        except Exception as e:
            LOG.exception(f"Unable to create Redis client. {e}")
            return None
        try:
            client.ping()
            LOG.info("Connected to Redis!")
        except Exception as e:
            LOG.warning(f"Redis is not reachable yet; reads fall back to the database until it is. {e}")
        return client

    def _ensure_connection(self):
        """The client to use, creating it if needed; None while Redis is disabled,
        unavailable or being skipped."""
        _check_not_on_event_loop()
        if not self.redis_client:
            LOG.warning("Redis client is None. Attempting to create new connection...")
            Cache._redis_client = self._create_redis_client()
            self.redis_client = Cache._redis_client
        if _cool_down.skipping():
            return None
        return self.redis_client

    def cache_installations(self, tenant_id, installations):
        if not self._ensure_connection():
            return
        key = f"notification_installations:{tenant_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                installations_dict = [installation.to_dict() for installation in installations]
                pipe.set(key, json.dumps(installations_dict, default=self._json_serializable))
                # redis EXPIRE takes SECONDS, so a minutes setting converts with * 60 —
                # as every other expire in this file does. This one used * 30, giving
                # installations half the configured lifetime.
                pipe.expire(key, settings.redis.cache_expiration_minutes * 60)
                pipe.execute()
            except TypeError as e:
                LOG.exception(f"Error serializing installations: {e}")
            except redis.RedisError as e:
                LOG.exception(f"Error caching installations for tenant {tenant_id}: {e}")

    def get_installations(self, tenant_id):
        if not self._ensure_connection():
            return None
        key = f"notification_installations:{tenant_id}"
        try:
            installations_json = self.redis_client.get(key)
        except redis.RedisError as e:
            LOG.warning(f"Error reading cached installations for tenant {tenant_id}: {e}")
            return None
        if installations_json:
            installations_data = json.loads(installations_json)
            return [MessagingPlatform.from_dict(data) for data in installations_data]
        return None

    def delete_cached_installations(self, tenant_id):
        if not self._ensure_connection():
            return
        key = f"notification_installations:{tenant_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.delete(key)
                pipe.execute()
                LOG.info(f"Cache entry for key '{key}' deleted successfully.")
            except redis.RedisError as e:
                LOG.exception(f"Error deleting cache entry for key '{key}': {e}")

    def cache_notification_rules(self, tenant_id, rules):
        if not self._ensure_connection():
            return
        key = f"notification_rules:v5:{tenant_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                rules_dict = [rule.to_dict() if hasattr(rule, "to_dict") else rule for rule in rules]
                pipe.set(key, json.dumps(rules_dict, default=self._json_serializable))
                pipe.expire(key, settings.redis.cache_expiration_minutes * 60)
                pipe.execute()
            except TypeError as e:
                LOG.exception(f"Error serializing notification rules: {e}")
            except redis.RedisError as e:
                LOG.exception(f"Error caching notification rules for tenant {tenant_id}: {e}")

    def get_notification_rules(self, tenant_id):
        if not self._ensure_connection():
            return None
        key = f"notification_rules:v5:{tenant_id}"
        try:
            rules_json = self.redis_client.get(key)
        except redis.RedisError as e:
            LOG.warning(f"Error reading cached notification rules for tenant {tenant_id}: {e}")
            return None
        if rules_json:
            rules_data = json.loads(rules_json)
            from notifications_server.models.models import NotificationRules

            return [NotificationRules.from_dict(data) for data in rules_data]
        return None

    def delete_cached_notification_rules(self, tenant_id):
        if not self._ensure_connection():
            return
        key = f"notification_rules:v5:{tenant_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.delete(key)
                pipe.delete(f"notification_defaults:v1:{tenant_id}")
                # Earlier keys too, for older pods still running during a rollout.
                pipe.delete(f"notification_rules:v4:{tenant_id}")
                pipe.delete(f"notification_rules:v3:{tenant_id}")
                pipe.delete(f"notification_rules:v2:{tenant_id}")
                pipe.delete(f"notification_rules:{tenant_id}")
                pipe.execute()
                LOG.info(f"Cache entry for key '{key}' deleted successfully.")
            except redis.RedisError as e:
                LOG.exception(f"Error deleting cache entry for key '{key}': {e}")

    def cache_default_policies(self, tenant_id, policies):
        if not self._ensure_connection():
            return
        key = f"notification_defaults:v1:{tenant_id}"
        try:
            self.redis_client.set(key, json.dumps(policies), ex=settings.redis.cache_expiration_minutes * 60)
        except redis.RedisError as e:
            LOG.exception(f"Error caching notification default policies for tenant {tenant_id}: {e}")

    def get_default_policies(self, tenant_id):
        if not self._ensure_connection():
            return None
        try:
            cached = self.redis_client.get(f"notification_defaults:v1:{tenant_id}")
        except redis.RedisError as e:
            LOG.warning(f"Error reading cached notification default policies for tenant {tenant_id}: {e}")
            return None
        return json.loads(cached) if cached else None

    def cache_event_entry(self, thread_ts, event_entry):
        if not self._ensure_connection():
            return
        key = f"chat_event:{thread_ts}"
        with self.redis_client.pipeline() as pipe:
            try:
                event_entry["timestamp"] = time.time()
                pipe.set(key, json.dumps(event_entry, default=self._json_serializable))
                pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                pipe.execute()
            except TypeError as e:
                LOG.exception(f"Error serializing event entry: {e}")
            except redis.RedisError as e:
                LOG.exception(f"Error caching event entry {thread_ts}: {e}")

    def get_event_entry(self, thread_ts):
        if not self._ensure_connection():
            return None
        key = f"chat_event:{thread_ts}"
        try:
            event_json = self.redis_client.get(key)
            if event_json:
                event_entry = json.loads(event_json)
                timestamp = event_entry.get("timestamp")
                if timestamp and time.time() - timestamp <= settings.redis.conversation_cache_expiration_minutes * 60:
                    return event_entry
                else:
                    self.redis_client.delete(key)
        except redis.RedisError as e:
            LOG.warning(f"Error reading cached event entry {thread_ts}: {e}")
        return None

    def _json_serializable(self, obj):
        """Helper function to serialize non-JSON serializable objects like UUID."""
        if isinstance(obj, uuid.UUID):
            return str(obj)
        raise TypeError(f"Object of type {obj.__class__.__name__} is not JSON serializable")

    def update_event_entry(self, thread_ts, **kwargs):
        """Merge `kwargs` into the cached entry. Safe against concurrent callers
        updating different fields on the same entry (e.g. the Slack progress
        poller's own thread claiming stream_ts while the request's main thread
        writes channel_context_used) — WATCH detects a conflicting write between
        our read and write and retries with a fresh copy, instead of silently
        clobbering it with a stale merge."""
        if not self._ensure_connection():
            return False
        key = f"chat_event:{thread_ts}"
        updates = {k: v for k, v in kwargs.items() if v is not None}
        for _ in range(_UPDATE_EVENT_ENTRY_MAX_RETRIES):
            try:
                with self.redis_client.pipeline() as pipe:
                    pipe.watch(key)
                    event_json = pipe.get(key)
                    if not event_json:
                        pipe.unwatch()
                        return False
                    event_entry = json.loads(event_json)
                    event_entry.update(updates)
                    event_entry["timestamp"] = time.time()
                    pipe.multi()
                    pipe.set(key, json.dumps(event_entry, default=self._json_serializable))
                    pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                    pipe.execute()
                    return True
            except redis.WatchError:
                continue
            except redis.RedisError as e:
                LOG.exception(f"Error updating event entry {thread_ts}: {e}")
                return False
        LOG.warning(
            "Giving up updating event entry %s after %d retries (concurrent writer contention)",
            thread_ts,
            _UPDATE_EVENT_ENTRY_MAX_RETRIES,
        )
        return False

    def remove_event_keys(self, thread_ts, keys):
        # update_event_entry can't clear a field: it drops None values so other
        # callers can do partial updates. Deleting keys needs an explicit path.
        if not self._ensure_connection():
            return False
        key = f"chat_event:{thread_ts}"
        try:
            event_json = self.redis_client.get(key)
            if not event_json:
                return False
            event_entry = json.loads(event_json)
            for k in keys:
                event_entry.pop(k, None)
            event_entry["timestamp"] = time.time()
            with self.redis_client.pipeline() as pipe:
                pipe.set(key, json.dumps(event_entry, default=self._json_serializable))
                pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                pipe.execute()
            return True
        except (redis.RedisError, json.JSONDecodeError) as e:
            LOG.exception(f"Error removing keys from event entry {thread_ts}: {e}")
            return False

    def take_teams_thinking(self, thread_ts, claim):
        """Clear this turn's Teams Thinking card from the entry and return its
        activity id, but only while claim is still the entry's claim, so a newer
        turn's card is never taken. None when it isn't."""
        if not self._ensure_connection() or not claim:
            return None
        key = f"chat_event:{thread_ts}"
        for _ in range(_UPDATE_EVENT_ENTRY_MAX_RETRIES):
            try:
                with self.redis_client.pipeline() as pipe:
                    pipe.watch(key)
                    event_json = pipe.get(key)
                    event_entry = json.loads(event_json) if event_json else None
                    if not event_entry or event_entry.get("thinking_claim") != claim:
                        pipe.unwatch()
                        return None
                    activity_id = event_entry.pop("thinking_id", None)
                    event_entry.pop("thinking_claim", None)
                    event_entry.pop("thinking_at", None)
                    event_entry["timestamp"] = time.time()
                    pipe.multi()
                    pipe.set(key, json.dumps(event_entry, default=self._json_serializable))
                    pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                    pipe.execute()
                    return activity_id
            except redis.WatchError:
                continue
            except (redis.RedisError, json.JSONDecodeError) as e:
                LOG.exception(f"Error taking the Teams Thinking card from {thread_ts}: {e}")
                return None
        return None

    def lock_teams_message(self, activity_id, token, ttl_seconds):
        """Serialises updates to one Teams message. True when token now holds it.
        Fails open (True) when Redis is unavailable."""
        if not self._ensure_connection():
            return True
        try:
            return bool(self.redis_client.set(f"teams_message_lock:{activity_id}", token, nx=True, ex=ttl_seconds))
        except redis.RedisError as e:
            LOG.exception(f"Error locking Teams message {activity_id}: {e}")
            return True

    def unlock_teams_message(self, activity_id, token):
        """Releases the lock only if token still holds it."""
        if not self._ensure_connection():
            return
        key = f"teams_message_lock:{activity_id}"
        try:
            with self.redis_client.pipeline() as pipe:
                pipe.watch(key)
                value = pipe.get(key)
                if (value.decode() if isinstance(value, bytes) else value) != token:
                    pipe.unwatch()
                    return
                pipe.multi()
                pipe.delete(key)
                pipe.execute()
        except redis.WatchError:
            return
        except redis.RedisError as e:
            LOG.exception(f"Error unlocking Teams message {activity_id}: {e}")

    def remove_event_entry(self, thread_ts):
        if not self._ensure_connection():
            return False
        key = f"chat_event:{thread_ts}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.delete(key)
                pipe.execute()
                return True
            except redis.RedisError as e:
                LOG.exception(f"Error removing event entry {thread_ts}: {e}")
                return False

    def cache_thread_images(self, thread_ts, images):
        # The current turn's images live under their own key (not the main
        # chat_event entry) so the potentially-large base64 blob isn't
        # re-serialized every time the entry's text/state is updated. Written on
        # every turn — including an empty list — so a later image-less turn does
        # not resend a prior turn's image.
        if not self._ensure_connection():
            return
        key = f"chat_images:{thread_ts}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.set(key, json.dumps(images))
                pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                pipe.execute()
            except (TypeError, redis.RedisError) as e:
                LOG.exception(f"Error caching thread images {thread_ts}: {e}")

    def get_thread_images(self, thread_ts):
        if not self._ensure_connection():
            return []
        key = f"chat_images:{thread_ts}"
        try:
            images_json = self.redis_client.get(key)
        except redis.RedisError as e:
            LOG.exception(f"Error retrieving thread images {thread_ts}: {e}")
            return []
        if images_json:
            try:
                return json.loads(images_json)
            except json.JSONDecodeError:
                return []
        return []

    def cache_thread_mentions(self, thread_ts, user_ids):
        # The people a question points at ("what did @john say") can only be read
        # from the raw event text — the cleaned query has had every <@U…> token
        # stripped by the time retrieval sees it. Stashed per turn, alongside the
        # images, for the same reason: it belongs to this turn, not the entry.
        if not self._ensure_connection():
            return
        key = f"chat_mentions:{thread_ts}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.set(key, json.dumps(user_ids))
                pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                pipe.execute()
            except (TypeError, redis.RedisError) as e:
                LOG.exception(f"Error caching thread mentions {thread_ts}: {e}")

    def get_thread_mentions(self, thread_ts):
        """Reads and clears. Button and dropdown followups reach retrieval
        without re-stashing, and inheriting a previous turn's targets would make
        Nubi answer "what did @john say" for a question that named nobody."""
        if not self._ensure_connection():
            return []
        key = f"chat_mentions:{thread_ts}"
        try:
            raw = self.redis_client.get(key)
            self.redis_client.delete(key)
        except redis.RedisError as e:
            LOG.exception(f"Error retrieving thread mentions {thread_ts}: {e}")
            return []
        if raw:
            try:
                return json.loads(raw)
            except json.JSONDecodeError:
                return []
        return []

    # A watch runs up to 24h — longer than the conversation cache TTL — so the
    # status-message coords need their own key. 25h = small margin over that.
    WATCH_STATUS_TTL_SECONDS = 25 * 60 * 60

    def cache_watch_status(self, watch_id, status_data, nx=False):
        """Store the 'watching…' message coords (channel/thread/status_msg_ts/team/
        platform) under watch_id so the terminal callback can retire it.
        nx=True -> atomic SET NX, doubling as a claim: only one concurrent
        watch_registered wins. False if already claimed or on any Redis error."""
        if not self._ensure_connection() or not watch_id:
            return False
        key = f"watch_status:{watch_id}"
        try:
            status_data = dict(status_data)
            status_data["timestamp"] = time.time()
            value = json.dumps(status_data, default=self._json_serializable)
            # One SET w/ `ex` -> value + expiry land atomically (SET/EXPIRE could
            # strand a TTL-less key). With nx it is also the claim: None if taken.
            return bool(self.redis_client.set(key, value, nx=nx, ex=self.WATCH_STATUS_TTL_SECONDS))
        except (TypeError, redis.RedisError) as e:
            LOG.exception(f"Error caching watch status for {watch_id}: {e}")
            return False

    def get_watch_status(self, watch_id):
        """Return the stored watch status coordinates, or None if absent/expired."""
        if not self._ensure_connection() or not watch_id:
            return None
        key = f"watch_status:{watch_id}"
        try:
            status_json = self.redis_client.get(key)
        except redis.RedisError as e:
            LOG.exception(f"Error retrieving watch status for {watch_id}: {e}")
            return None
        if status_json:
            try:
                return json.loads(status_json)
            except json.JSONDecodeError:
                return None
        return None

    def remove_watch_status(self, watch_id):
        """Drop the watch status entry once its message has been finalized."""
        if not self._ensure_connection() or not watch_id:
            return False
        key = f"watch_status:{watch_id}"
        try:
            self.redis_client.delete(key)
            return True
        except redis.RedisError as e:
            LOG.exception(f"Error removing watch status for {watch_id}: {e}")
            return False

    def cache_channel_session_mapping(self, channel_id, team_id, session_id, account_id=None, tenant_id=None):
        """Cache the mapping between channel_id and session_id from /channels/join"""
        if not self._ensure_connection():
            return False
        key = f"channel_session:{team_id}:{channel_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                mapping_data = {
                    "session_id": session_id,
                    "account_id": account_id,
                    "tenant_id": tenant_id,
                    "timestamp": time.time(),
                }
                pipe.set(key, json.dumps(mapping_data, default=self._json_serializable))
                pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                pipe.execute()
                LOG.debug(
                    f"Cached channel session mapping: {channel_id} -> session_id={session_id}, "
                    f"account_id={account_id}, tenant_id={tenant_id}"
                )
                return True
            except redis.RedisError as e:
                LOG.exception(f"Error caching channel session mapping for {channel_id}: {e}")
                return False

    def get_channel_session_mapping(self, channel_id, team_id):
        """Get the session_id and account details associated with a channel from /channels/join

        Returns:
            dict with keys: session_id, account_id, tenant_id (or None if not found/expired)
        """
        if not self._ensure_connection():
            return None
        key = f"channel_session:{team_id}:{channel_id}"
        try:
            mapping_json = self.redis_client.get(key)
            if mapping_json:
                mapping_data = json.loads(mapping_json)
                timestamp = mapping_data.get("timestamp")
                if timestamp and time.time() - timestamp <= settings.redis.conversation_cache_expiration_minutes * 60:
                    return {
                        "session_id": mapping_data.get("session_id"),
                        "account_id": mapping_data.get("account_id"),
                        "tenant_id": mapping_data.get("tenant_id"),
                    }
                else:
                    self.redis_client.delete(key)
        except redis.RedisError as e:
            LOG.exception(f"Error retrieving channel session mapping for {channel_id}: {e}")
        return None

    def remove_channel_session_mapping(self, channel_id, team_id):
        """Remove the channel-to-session_id mapping"""
        if not self._ensure_connection():
            return False
        key = f"channel_session:{team_id}:{channel_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.delete(key)
                pipe.execute()
                LOG.info(f"Removed channel session mapping for {channel_id}")
                return True
            except redis.RedisError as e:
                LOG.exception(f"Error removing channel session mapping for {channel_id}: {e}")
                return False

    # --- Channel-awareness watched-set mirror -------------------------------
    # Mirror of the enabled rows in messaging_channel_watch, one Redis set per
    # workspace, maintained on every toggle. No TTL: the DB is authoritative and
    # rebuild_watched_channels reseeds after a Redis flush. Readers must treat
    # None (Redis unavailable) as "unknown" and fall back to the DB.

    @staticmethod
    def _watched_channels_key(platform, team_id):
        return f"watched_channels:{platform}:{team_id}"

    def add_watched_channel(self, platform, team_id, channel_id):
        if not self._ensure_connection():
            return False
        try:
            self.redis_client.sadd(self._watched_channels_key(platform, team_id), channel_id)
            return True
        except redis.RedisError as e:
            LOG.exception(f"Error adding watched channel {channel_id}: {e}")
            return False

    def remove_watched_channel(self, platform, team_id, channel_id):
        if not self._ensure_connection():
            return False
        try:
            self.redis_client.srem(self._watched_channels_key(platform, team_id), channel_id)
            return True
        except redis.RedisError as e:
            LOG.exception(f"Error removing watched channel {channel_id}: {e}")
            return False

    def is_channel_watched(self, platform, team_id, channel_id):
        """True/False from the mirror, or None when Redis is unavailable so the
        caller can fall back to the messaging_channel_watch table."""
        if not self._ensure_connection():
            return None
        try:
            return bool(self.redis_client.sismember(self._watched_channels_key(platform, team_id), channel_id))
        except redis.RedisError as e:
            LOG.exception(f"Error checking watched channel {channel_id}: {e}")
            return None

    def get_cached_user_name(self, team_id, user_id):
        if not self._ensure_connection():
            return None
        try:
            return self.redis_client.get(f"slack_user_name:{team_id}:{user_id}")
        except redis.RedisError as e:
            LOG.exception(f"Error reading cached user name for {user_id}: {e}")
            return None

    def cache_user_name(self, team_id, user_id, display_name, ttl_seconds=86400):
        if not self._ensure_connection() or not display_name:
            return False
        try:
            self.redis_client.set(f"slack_user_name:{team_id}:{user_id}", display_name, ex=ttl_seconds)
            return True
        except redis.RedisError as e:
            LOG.exception(f"Error caching user name for {user_id}: {e}")
            return False

    def get_cached_team_domain(self, team_id):
        if not self._ensure_connection():
            return None
        try:
            return self.redis_client.get(f"slack_team_domain:{team_id}")
        except redis.RedisError as e:
            LOG.exception(f"Error reading cached team domain for {team_id}: {e}")
            return None

    def cache_team_domain(self, team_id, domain, ttl_seconds=86400):
        """Empty string is a valid value — it negative-caches a workspace whose
        domain could not be resolved, so failures don't retry the API per mention."""
        if not self._ensure_connection() or domain is None:
            return False
        try:
            self.redis_client.set(f"slack_team_domain:{team_id}", domain, ex=ttl_seconds)
            return True
        except redis.RedisError as e:
            LOG.exception(f"Error caching team domain for {team_id}: {e}")
            return False

    def mark_event_seen(self, event_id, ttl_seconds=600):
        """True the first time an event id is seen, False on Slack's retries.
        Fails open (True) when Redis is unavailable — the storage layer upserts,
        so a duplicate is harmless, whereas dropping a real message is not."""
        if not self._ensure_connection():
            return True
        try:
            return bool(self.redis_client.set(f"channel_event:{event_id}", "1", nx=True, ex=ttl_seconds))
        except redis.RedisError as e:
            LOG.exception(f"Error deduping event {event_id}: {e}")
            return True

    def claim_card_action(self, key, ttl_seconds):
        """First click on a card action wins (True) until the claim expires.
        Only a fast path: the stored card is what refuses repeats. Fails open
        (True) when Redis is unavailable."""
        if not self._ensure_connection():
            return True
        try:
            return bool(self.redis_client.set(f"card_action:{key}", "1", nx=True, ex=ttl_seconds))
        except redis.RedisError as e:
            LOG.exception(f"Error claiming card action {key}: {e}")
            return True

    def release_card_action(self, key):
        """Drops a claim taken by claim_card_action, so a retry after a failure
        isn't ignored until the claim expires."""
        if not self._ensure_connection():
            return
        try:
            self.redis_client.delete(f"card_action:{key}")
        except redis.RedisError as e:
            LOG.exception(f"Error releasing card action {key}: {e}")

    def claim_event_analysis_poller(self, event_id, ttl_seconds):
        """First claim for event_id wins (True); a second, still-live claim
        gets False -- guards against two overlapping clicks on the same event
        each spawning their own poller. Fails open (True) when Redis is
        unavailable, so an outage degrades to "no dedup" rather than
        silently dropping the poller."""
        if not self._ensure_connection():
            return True
        try:
            return bool(self.redis_client.set(f"event_analysis_poll:{event_id}", "1", nx=True, ex=ttl_seconds))
        except redis.RedisError as e:
            LOG.exception(f"Error claiming event analysis poller for {event_id}: {e}")
            return True

    def claim_teams_analysis(self, event_id, ttl_seconds, token):
        """First Teams Ask Nubi click on an event wins (True) while the claim
        lives; it's the only "still analyzing" signal, kept alive by
        refresh_teams_analysis. token identifies the worker that owns it.
        Separate from Slack's claim, so neither platform silences the other.
        Fails open (True) when Redis is unavailable."""
        if not self._ensure_connection():
            return True
        try:
            return bool(self.redis_client.set(_teams_analysis_key(event_id), token, nx=True, ex=ttl_seconds))
        except redis.RedisError as e:
            LOG.exception(f"Error claiming Teams event analysis for {event_id}: {e}")
            return True

    def _teams_analysis_owner(self, event_id):
        value = self.redis_client.get(_teams_analysis_key(event_id))
        return value.decode() if isinstance(value, bytes) else value

    def refresh_teams_analysis(self, event_id, ttl_seconds, token):
        """Keeps a live worker's claim from expiring, re-taking it if it lapsed
        and nobody else took it; a dead worker's runs out."""
        if not self._ensure_connection():
            return
        try:
            if self._teams_analysis_owner(event_id) in (None, token):
                self.redis_client.set(_teams_analysis_key(event_id), token, ex=ttl_seconds)
        except redis.RedisError as e:
            LOG.exception(f"Error refreshing Teams event analysis for {event_id}: {e}")

    def release_teams_analysis(self, event_id, token):
        """Releases the claim only if this worker still owns it."""
        if not self._ensure_connection():
            return
        try:
            if self._teams_analysis_owner(event_id) == token:
                self.redis_client.delete(_teams_analysis_key(event_id))
        except redis.RedisError as e:
            LOG.exception(f"Error releasing Teams event analysis for {event_id}: {e}")

    def release_event_analysis_poller(self, event_id):
        """Releases a claim taken by claim_event_analysis_poller -- without
        this, a claim only ever clears on its own after the full TTL, so any
        retry for the same event stays blocked for that long even though no
        poller is actually running any more."""
        if not self._ensure_connection():
            return
        try:
            self.redis_client.delete(f"event_analysis_poll:{event_id}")
        except redis.RedisError as e:
            LOG.exception(f"Error releasing event analysis poller for {event_id}: {e}")

    def rebuild_watched_channels(self, platform, team_id, channel_ids):
        """Reseed one workspace's mirror from the DB truth (e.g. after a flush)."""
        if not self._ensure_connection():
            return False
        key = self._watched_channels_key(platform, team_id)
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.delete(key)
                if channel_ids:
                    pipe.sadd(key, *channel_ids)
                pipe.execute()
                return True
            except redis.RedisError as e:
                LOG.exception(f"Error rebuilding watched channels for {team_id}: {e}")
                return False
