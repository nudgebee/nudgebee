"""Replica broadcast (issue #40807)."""

import json
import os
import queue
import time
from pathlib import Path

import pytest

from rag.core import cache_backend as cb
from rag.core import replica_broadcast as rb
from rag.core.cache import get_collection_list_cache
from rag.core.metadata_cache import get_metadata_cache
from utils.config import Config


@pytest.fixture(autouse=True)
def _in_memory(monkeypatch):
    monkeypatch.setattr(Config, "cache_provider", "in_memory")
    cb.reset_cache_backend()
    yield
    cb.reset_cache_backend()


def _record(change_type, request):
    calls = {"keys": [], "all": 0}

    def on_all():
        calls["all"] += 1

    request.addfinalizer(rb.on_replica_broadcast(change_type, on_keys=calls["keys"].append, on_all=on_all))
    return calls


def test_keys_go_to_on_keys_and_no_keys_to_on_all(request):
    mine = _record("test_local_changed", request)
    other = _record("test_other_changed", request)

    rb.replica_broadcast("test_local_changed", "kb-1", "kb-2")
    rb.replica_broadcast_all("test_local_changed")

    assert mine == {"keys": [["kb-1", "kb-2"]], "all": 1}
    assert other == {"keys": [], "all": 0}


def test_both_callbacks_are_required():
    with pytest.raises(ValueError):
        rb.on_replica_broadcast("test_missing_changed", on_keys=lambda keys: None, on_all=None)


def test_unregister_removes_the_handler():
    calls = []
    unregister = rb.on_replica_broadcast(
        "test_unregister_changed", on_keys=calls.append, on_all=lambda: calls.append("all")
    )
    unregister()

    rb.replica_broadcast("test_unregister_changed", "k")
    assert calls == []


def test_collection_change_drops_local_metadata():
    meta = get_metadata_cache()
    meta.set("kb-a", {"account": "1"})
    meta.set("kb-b", {"account": "2"})

    # What another pod's message does when it arrives here.
    rb._dispatch(rb.CHANGE_RAG_COLLECTIONS, ["kb-a"])
    assert meta.get("kb-a") is None
    assert meta.get("kb-b") is not None

    rb._dispatch(rb.CHANGE_RAG_COLLECTIONS, [])
    assert meta.get("kb-b") is None


def test_collection_change_drops_local_collection_list():
    cache = get_collection_list_cache()
    cache.drop_local()
    with cache._lock:
        cache._collections = []
        cache._timestamp = time.time()

    rb._dispatch(rb.CHANGE_RAG_COLLECTIONS, ["kb-a"])

    with cache._lock:
        assert cache._collections is None
        assert cache._timestamp == 0.0


def test_message_matches_shared_fixture(request):
    """The wire format is shared with the Go services; docs/fixtures/replica_message.json
    is the one copy of it."""
    fixture = json.loads((Path(__file__).parents[3] / "docs/fixtures/replica_message.json").read_text())
    published = []

    class _Publisher:
        def publish(self, channel, payload):
            published.append(json.loads(payload))

    request.addfinalizer(lambda: setattr(rb, "_publisher", None))
    rb._publisher = _Publisher()
    original = rb._redis_enabled
    rb._redis_enabled = lambda: True
    request.addfinalizer(lambda: setattr(rb, "_redis_enabled", original))

    rb.replica_broadcast("example_changed", "00000000-0000-0000-0000-000000000001")

    assert set(published[0]) == set(fixture)
    assert published[0]["keys"] == fixture["keys"]
    assert published[0]["sender"].startswith("rag-server/")


@pytest.mark.skipif(not os.environ.get("NB_TEST_REDIS_ADDR"), reason="NB_TEST_REDIS_ADDR not set")
def test_subscriber_against_redis(monkeypatch, request):
    """A message from another pod reaches on_keys, this pod's own message is
    skipped, and a dropped connection runs on_all."""
    import redis

    host, port = os.environ["NB_TEST_REDIS_ADDR"].split(":")
    monkeypatch.setattr(Config, "cache_provider", "redis")
    monkeypatch.setattr(Config, "cache_redis_host", host)
    monkeypatch.setattr(Config, "cache_redis_port", int(port))

    keys, alls = queue.Queue(), queue.Queue()
    request.addfinalizer(rb.on_replica_broadcast("test_redis_changed", on_keys=keys.put, on_all=lambda: alls.put(True)))
    rb.start_replica_broadcast_subscriber()
    assert alls.get(timeout=10), "initial resync"

    other = redis.Redis(host=host, port=int(port), decode_responses=True)
    other.publish(rb.CHANNEL, json.dumps({"type": "test_redis_changed", "keys": ["self"], "sender": rb.SENDER}))
    other.publish(rb.CHANNEL, '{"type":"test_redis_changed","keys":["t9"],"sender":"services-server/pod-b"}')
    assert keys.get(timeout=10) == ["t9"], "this pod's own message must be skipped"

    assert other.execute_command("CLIENT", "KILL", "TYPE", "pubsub") >= 1
    assert alls.get(timeout=10), "resync after connection drop"
