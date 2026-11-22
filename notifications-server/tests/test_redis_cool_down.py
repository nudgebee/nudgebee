"""
After a connection error or timeout ends a Redis operation, Redis is skipped for
a cool-down: reads miss, writes are dropped and claims fail open without opening a
socket, so an unreachable Redis costs one wait rather than one per call. When the
cool-down ends, one caller probes Redis and the rest keep skipping until it settles.
Async code reaches the cache through Cache.aio, which never runs Redis on the loop.
"""

import asyncio
import logging
import socket
import threading

import pytest
import redis

from notifications_server.configs import settings
from notifications_server.services import cache as cache_module
from notifications_server.services.cache import Cache, RedisCoolingDown

THREAD = "1700000000.000100"


@pytest.fixture
def connects(monkeypatch):
    """Counts the sockets redis-py tries to open."""
    calls = []
    real_connect = redis.connection.Connection._connect

    def counting_connect(self):
        calls.append(self.port)
        return real_connect(self)

    monkeypatch.setattr(redis.connection.Connection, "_connect", counting_connect)
    return calls


@pytest.fixture
def closed_port_cache(monkeypatch):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        free_port = sock.getsockname()[1]
    monkeypatch.setattr(settings.redis, "host", "127.0.0.1")
    monkeypatch.setattr(settings.redis, "port", free_port)
    client = Cache._create_redis_client()
    # Creating the client pings Redis, which already starts the cool-down.
    cache_module._cool_down.reset()
    cache = Cache()
    cache.redis_client = client
    yield cache
    client.close()


def _retry():
    return cache_module._CoolDownRetry(redis.backoff.NoBackoff(), 3)


def _connection_refused():
    raise redis.exceptions.ConnectionError("Connection refused")


def test_after_a_connection_error_redis_is_skipped_without_opening_a_socket(closed_port_cache, connects):
    cache = closed_port_cache

    assert cache.get_event_entry(THREAD) is None
    assert connects, "the first call should have tried to reach Redis"
    assert cache_module._cool_down.skipping()

    connects.clear()
    assert cache.get_event_entry(THREAD) is None
    assert cache.update_event_entry(THREAD, text="hi") is False
    assert cache.cache_event_entry(THREAD, {"text": "hi"}) is None
    assert cache.get_notification_rules("tenant") is None
    assert cache.claim_card_action("card", 60) is True
    assert cache.mark_event_seen("event") is True
    assert connects == []


def test_the_raw_client_also_fails_fast_while_redis_is_skipped(closed_port_cache, connects):
    cache = closed_port_cache
    cache.get_event_entry(THREAD)
    connects.clear()

    with pytest.raises(RedisCoolingDown):
        cache.redis_client.llen("grouped:tenant:slo_alert")
    assert connects == []


def test_a_skipped_read_logs_nothing_per_call(closed_port_cache, caplog):
    cache = closed_port_cache
    cache.get_event_entry(THREAD)

    with caplog.at_level(logging.WARNING, logger=cache_module.LOG.name):
        caplog.clear()
        for _ in range(5):
            cache.get_event_entry(THREAD)

    assert caplog.records == []


def test_when_the_cool_down_ends_one_probe_goes_through_and_its_success_reopens_redis():
    cache_module._cool_down.trip(redis.exceptions.ConnectionError("down"))
    cache_module._cool_down._until = 0.001  # long past
    seen_by_another_caller = []

    def probe():
        def other_caller():
            try:
                _retry().call_with_retry(lambda: "reached Redis", lambda _e: None)
            except RedisCoolingDown:
                seen_by_another_caller.append("skipped")

        thread = threading.Thread(target=other_caller)
        thread.start()
        thread.join()
        return "pong"

    assert _retry().call_with_retry(probe, lambda _e: None) == "pong"
    assert seen_by_another_caller == ["skipped"]
    assert not cache_module._cool_down.skipping()


def test_a_recovery_that_lands_while_waiting_to_probe_keeps_redis_open():
    cool_down = cache_module._CoolDown()
    cool_down._until = 0.001  # expired

    class _LockTakenDuringRecovery:
        def __enter__(self):
            cool_down._until = 0.0  # another thread's probe succeeded first

        def __exit__(self, *_exc):
            return False

    cool_down._lock = _LockTakenDuringRecovery()

    assert cool_down.admit() is True
    assert not cool_down.skipping()
    assert cool_down._until == 0.0


def test_a_failed_probe_restarts_the_cool_down():
    cache_module._cool_down.trip(redis.exceptions.ConnectionError("down"))
    cache_module._cool_down._until = 0.001

    with pytest.raises(redis.exceptions.ConnectionError):
        _retry().call_with_retry(_connection_refused, lambda _e: None)

    assert cache_module._cool_down.skipping()


def test_a_lost_connection_while_watching_still_starts_the_cool_down():
    # redis-py's failure handler raises WatchError while a key is WATCHed, and
    # update_event_entry retries a WatchError; the cool-down must start anyway.
    def watching_failure(_error, *_args):
        raise redis.exceptions.WatchError("A ConnectionError occurred while watching one or more keys")

    with pytest.raises(redis.exceptions.WatchError):
        _retry().call_with_retry(_connection_refused, watching_failure)

    assert cache_module._cool_down.skipping()


def test_an_operation_under_way_stops_retrying_once_redis_is_skipped():
    attempts, failures_handled = [], []

    def command():
        attempts.append(1)
        # A nested retry (a reconnect inside the command) gives up first.
        _retry().call_with_retry(_connection_refused, lambda _e: None)

    with pytest.raises(RedisCoolingDown):
        _retry().call_with_retry(command, failures_handled.append)

    assert attempts == [1]
    # The nested failure is handled once; the operation then ends without
    # another attempt, handler call or backoff.
    assert len(failures_handled) == 1
    assert cache_module._cool_down.skipping()


def test_errors_that_are_not_outages_do_not_start_the_cool_down():
    def wrong_type():
        raise redis.exceptions.ResponseError("WRONGTYPE")

    with pytest.raises(redis.exceptions.ResponseError):
        _retry().call_with_retry(wrong_type, lambda _e: None)

    assert not cache_module._cool_down.skipping()


class _RecordingRedis:
    def __init__(self):
        self.threads = []

    def get(self, _key):
        self.threads.append(threading.current_thread().name)
        return None

    def set(self, *_args, **_kwargs):
        self.threads.append(threading.current_thread().name)
        return True


def test_aio_runs_each_call_on_the_cache_threads():
    cache = Cache()
    cache.redis_client = _RecordingRedis()

    async def calls():
        return await cache.aio.get_event_entry(THREAD), await cache.aio.claim_card_action("card", 60)

    assert asyncio.run(calls()) == (None, True)
    assert len(cache.redis_client.threads) == 2
    assert all(name.startswith("redis-cache") for name in cache.redis_client.threads)


def test_a_sync_call_on_the_event_loop_is_refused_under_tests():
    cache = Cache()
    cache.redis_client = _RecordingRedis()

    async def on_the_loop():
        cache.get_event_entry(THREAD)

    with pytest.raises(RuntimeError, match="cache.aio"):
        asyncio.run(on_the_loop())
    assert cache.redis_client.threads == []


def test_a_sync_call_on_the_event_loop_logs_a_warning_in_production(monkeypatch, caplog):
    monkeypatch.delenv(cache_module.FORBID_EVENT_LOOP_CALLS_ENV)
    monkeypatch.setattr(cache_module, "_warned_loop_sites", set())
    cache = Cache()
    cache.redis_client = _RecordingRedis()

    async def on_the_loop():
        for _ in range(3):
            cache.get_event_entry(THREAD)

    with caplog.at_level(logging.WARNING, logger=cache_module.LOG.name):
        asyncio.run(on_the_loop())

    warnings = [r for r in caplog.records if "event loop" in r.getMessage()]
    assert len(warnings) == 1
    assert __file__ in warnings[0].getMessage()
    assert len(cache.redis_client.threads) == 3
