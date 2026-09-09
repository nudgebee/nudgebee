"""Regression test for the lost-update race in Cache.update_event_entry.

Bug: update_event_entry did a plain GET -> modify in Python -> SET, with no
locking. Two threads updating different fields on the same cached entry
concurrently (e.g. the Slack progress poller's own thread claiming stream_ts
via _open_stream, racing the request's main thread writing
channel_context_used via _build_channel_context) could race: whichever SET
landed second silently overwrote the other's field with a stale snapshot.
Confirmed live: _open_stream's claim write lost the race, its own follow-up
check saw the claim "gone", and silently killed the panel it had just
opened -- with zero real tool calls ever polled for that turn (intermittent,
since it depends on how the two threads interleave).

Fix: WATCH the key before reading, MULTI/EXEC to write, and retry with a
fresh read if WATCH detects a conflicting write in between -- instead of
blindly overwriting whatever the other writer landed.
"""

import json
import sys
import types

# Bypass the heavy notifications_server/__init__.py (slack_bolt, msal, Postgres
# engine on import); we only need the Cache class for these unit tests.
_ROOT = "notifications_server"
if _ROOT not in sys.modules:
    sys.modules[_ROOT] = types.ModuleType(_ROOT)
    sys.modules[_ROOT].__path__ = [f"{__file__.rsplit('/', 2)[0]}/{_ROOT}"]  # noqa: SLF001

import redis  # noqa: E402

from notifications_server.services.cache import Cache  # noqa: E402

THREAD_TS = "C0AT6G26LUX-1779952241.483549"
KEY = f"chat_event:{THREAD_TS}"


class _FlakyPipeline:
    """Mimics a real WATCH/MULTI/EXEC pipeline: EXECUTE raises WatchError
    while watch_errors_remaining (shared across pipeline instances, one per
    retry attempt) is positive, simulating another writer touching the
    watched key between our WATCH and EXECUTE."""

    def __init__(self, store, watch_errors_remaining):
        self._store = store
        self._watch_errors_remaining = watch_errors_remaining
        self._queued = []

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def watch(self, key):
        pass

    def unwatch(self):
        pass

    def get(self, key):
        return self._store.get(key)

    def multi(self):
        self._queued = []

    def set(self, key, value):
        self._queued.append((key, value))

    def expire(self, key, ttl):
        pass

    def execute(self):
        if self._watch_errors_remaining[0] > 0:
            self._watch_errors_remaining[0] -= 1
            raise redis.WatchError("stale watch")
        for key, value in self._queued:
            self._store[key] = value
        return [True, True]


class _FlakyRedis:
    def __init__(self, watch_errors=0):
        self.store = {}
        self._watch_errors_remaining = [watch_errors]

    def pipeline(self):
        return _FlakyPipeline(self.store, self._watch_errors_remaining)


def _cache_with_entry(entry, watch_errors=0):
    cache = Cache()
    cache.redis_client = _FlakyRedis(watch_errors=watch_errors)
    cache._ensure_connection = lambda: None  # noqa: SLF001 - skip real connection
    cache.redis_client.store[KEY] = json.dumps(entry)
    return cache


def test_update_event_entry_retries_on_watch_error_and_succeeds():
    # e.g. the poller's own claim write racing the main thread's channel_context_used write.
    cache = _cache_with_entry({"stream_ts": "claim-1", "session_id": "s1"}, watch_errors=1)

    assert cache.update_event_entry(THREAD_TS, channel_context_used=["m1"]) is True

    entry = json.loads(cache.redis_client.store[KEY])
    assert entry["channel_context_used"] == ["m1"]
    # The other writer's field must not have been clobbered by a stale merge.
    assert entry["stream_ts"] == "claim-1"


def test_update_event_entry_gives_up_after_max_retries():
    cache = _cache_with_entry({"session_id": "s1"}, watch_errors=999)

    assert cache.update_event_entry(THREAD_TS, channel_context_used=["m1"]) is False
    # Never partially applied — the entry is exactly as it started.
    assert json.loads(cache.redis_client.store[KEY]) == {"session_id": "s1"}


def test_update_event_entry_missing_entry_returns_false():
    cache = Cache()
    cache.redis_client = _FlakyRedis()
    cache._ensure_connection = lambda: None  # noqa: SLF001
    assert cache.update_event_entry(THREAD_TS, foo="bar") is False
