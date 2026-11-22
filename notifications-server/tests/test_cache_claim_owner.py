"""A claim taken with claim_owned is released only by its owner (#40734): a holder whose claim expired and was taken
by the next sender must not release the next sender's claim. Fails open when Redis is unavailable."""

import os
import time

import pytest
import redis

from notifications_server.services.cache import Cache


class _Redis:
    """The Redis calls claim_owned and release_owned make, with expiry, and the release script's semantics."""

    def __init__(self):
        self.store, self.expires, self.scripts = {}, {}, []

    def _live(self, key):
        if key in self.expires and time.monotonic() >= self.expires[key]:
            self.store.pop(key, None)
            self.expires.pop(key, None)
        return key in self.store

    def set(self, key, value, nx=False, ex=None):
        if nx and self._live(key):
            return None
        self.store[key] = value
        if ex is not None:
            self.expires[key] = time.monotonic() + ex
        return True

    def eval(self, script, numkeys, key, token):
        self.scripts.append(script)
        assert "redis.call('get', KEYS[1]) == ARGV[1]" in script and "redis.call('del', KEYS[1])" in script
        if self._live(key) and self.store[key] == token:
            del self.store[key]
            return 1
        return 0


def _cache(client):
    cache = Cache()
    cache.redis_client = client
    cache._ensure_connection = lambda: cache.redis_client  # noqa: SLF001 - skip the real connection
    return cache


def test_a_late_release_does_not_remove_the_next_holders_claim():
    client = _Redis()
    a = _cache(client).claim_owned("k", 0.05)
    assert a
    assert _cache(client).claim_owned("k", 60) is None, "held"
    time.sleep(0.06)  # A's claim expires while A is still posting
    b = _cache(client).claim_owned("k", 60)
    assert b and b != a
    _cache(client).release_owned("k", a)  # A finishes late
    assert _cache(client).claim_owned("k", 60) is None, "B still holds it"
    _cache(client).release_owned("k", b)
    assert _cache(client).claim_owned("k", 60), "B's own release frees it"


def test_redis_unavailable_fails_open():
    cache = Cache()
    cache._ensure_connection = lambda: None  # noqa: SLF001
    assert cache.claim_owned("k", 60) == ""
    cache.release_owned("k", "")  # nothing to release, no error

    class Down(_Redis):
        def set(self, *a, **k):
            raise redis.ConnectionError("down")

        def eval(self, *a, **k):
            raise redis.ConnectionError("down")

    down = _cache(Down())
    assert down.claim_owned("k", 60) == ""
    down.release_owned("k", "some-token")


@pytest.mark.skipif(
    not os.getenv("REDIS_TEST_URL"), reason="set REDIS_TEST_URL to a disposable Redis to run the script itself"
)
def test_the_release_script_on_a_real_redis():
    client = redis.Redis.from_url(os.environ["REDIS_TEST_URL"], decode_responses=True)
    key = "claim-owner-test"
    client.delete(f"card_action:{key}")
    a = _cache(client).claim_owned(key, 1)
    time.sleep(1.1)
    b = _cache(client).claim_owned(key, 60)
    _cache(client).release_owned(key, a)
    assert client.get(f"card_action:{key}") == b
    _cache(client).release_owned(key, b)
    assert client.get(f"card_action:{key}") is None
