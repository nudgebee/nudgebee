"""Collection-list refresh fans metadata reads out instead of serialising them.

Qdrant's list endpoint returns names only, so every collection needs its own
get_collection() for the module/account metadata routing depends on. Done
sequentially that was ~8s for ~300 collections on dev, paid by whichever user
request happened to trip the 30-minute TTL.
"""

import threading
import time
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import pytest

from utils.config import Config


def _reset_caches(client_mod):
    from rag.core.cache import get_collection_list_cache
    from rag.core.metadata_cache import get_metadata_cache

    get_collection_list_cache().clear()
    get_metadata_cache().invalidate()
    with client_mod._singleflight_lock:
        client_mod._singleflight_event = None


def _stub(names, per_call_delay=0.0, failing=(), metadata_for=None):
    stub = MagicMock()
    stub.get_collections.side_effect = lambda: SimpleNamespace(collections=[SimpleNamespace(name=n) for n in names])

    def get_collection(name):
        if per_call_delay:
            time.sleep(per_call_delay)
        if name in failing:
            raise RuntimeError(f"boom for {name}")
        meta = (metadata_for or {}).get(name, {})
        return SimpleNamespace(config=SimpleNamespace(metadata=meta), points_count=7)

    stub.get_collection.side_effect = get_collection
    return stub


def test_metadata_reads_run_concurrently():
    """More than one metadata read must be in flight at a time."""
    from rag.qdrant import client as client_mod

    _reset_caches(client_mod)
    names = [f"c{i}" for i in range(40)]

    # Record how many fetches are in flight at once rather than how long the
    # whole thing took. A serial loop peaks at exactly 1 however slow the
    # machine is, so this cannot flake under CPU pressure the way a wall-clock
    # assertion does.
    lock = threading.Lock()
    state = {"active": 0, "peak": 0}

    stub = MagicMock()
    stub.get_collections.side_effect = lambda: SimpleNamespace(collections=[SimpleNamespace(name=n) for n in names])

    def get_collection(name):
        with lock:
            state["active"] += 1
            state["peak"] = max(state["peak"], state["active"])
        time.sleep(0.02)
        with lock:
            state["active"] -= 1
        return SimpleNamespace(config=SimpleNamespace(metadata={}), points_count=0)

    stub.get_collection.side_effect = get_collection

    with patch.object(client_mod, "get_qdrant_client", return_value=stub):
        result = client_mod.list_collections_optimized()

    assert len(result) == 40
    assert stub.get_collection.call_count == 40
    assert state["peak"] > 1, f"metadata fetch ran serially: peak concurrency was {state['peak']}"


def test_order_matches_the_listing():
    """Callers index into this list, so the fan-out must not reorder it."""
    from rag.qdrant import client as client_mod

    _reset_caches(client_mod)
    names = [f"c{i:03d}" for i in range(50)]
    stub = _stub(names)

    with patch.object(client_mod, "get_qdrant_client", return_value=stub):
        result = client_mod.list_collections_optimized()

    assert [c.name for c in result] == names


def test_metadata_and_points_count_survive_the_fanout():
    from rag.qdrant import client as client_mod

    _reset_caches(client_mod)
    names = ["a_events", "b_knowledge_base"]
    stub = _stub(names, metadata_for={"a_events": {"module": "events", "account": "acct-1"}})

    with patch.object(client_mod, "get_qdrant_client", return_value=stub):
        by_name = {c.name: c for c in client_mod.list_collections_optimized()}

    assert by_name["a_events"].metadata == {"module": "events", "account": "acct-1"}
    assert by_name["a_events"].points_count == 7
    assert by_name["b_knowledge_base"].metadata == {}


def test_one_unreadable_collection_does_not_drop_the_rest():
    """A collection we cannot describe still has to appear, or routing loses it."""
    from rag.qdrant import client as client_mod

    _reset_caches(client_mod)
    names = ["ok1", "broken", "ok2"]
    stub = _stub(names, failing={"broken"}, metadata_for={"ok1": {"module": "events"}})

    with patch.object(client_mod, "get_qdrant_client", return_value=stub):
        result = client_mod.list_collections_optimized()

    assert [c.name for c in result] == names
    broken = next(c for c in result if c.name == "broken")
    assert broken.metadata == {}
    assert broken.points_count == 0
    assert next(c for c in result if c.name == "ok1").metadata == {"module": "events"}


@pytest.mark.parametrize("count", [0, 1])
def test_small_listings_do_not_blow_up_the_pool(count):
    """max_workers must stay >= 1 even when there is nothing to fetch."""
    from rag.qdrant import client as client_mod

    _reset_caches(client_mod)
    names = [f"c{i}" for i in range(count)]

    with patch.object(client_mod, "get_qdrant_client", return_value=_stub(names)):
        assert len(client_mod.list_collections_optimized()) == count


def test_non_positive_worker_count_degrades_to_serial():
    """A 0 or negative worker count must still refresh, not crash.

    ThreadPoolExecutor rejects anything below 1, and this value comes from the
    environment, so an operator turning concurrency "off" would otherwise take
    every collection-list refresh down. Asserted against the real function with
    the value patched in, rather than by reloading the config module -- a reload
    would leave this module and the one under test holding different Config
    objects.
    """
    from rag.qdrant import client as client_mod

    names = ["a_events", "b_events"]
    for bad in (0, -4):
        _reset_caches(client_mod)
        with patch.object(Config, "collection_metadata_max_workers", bad):
            with patch.object(client_mod, "get_qdrant_client", return_value=_stub(names)):
                result = client_mod.list_collections_optimized()
        assert [c.name for c in result] == names, f"workers={bad} should still return every collection"
