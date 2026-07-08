"""Single-flight coordination in list_collections_optimized.

The bug we're guarding against: concurrent cache MISSes on the collection list
would each fire the slow list+metadata iteration against Qdrant (5 concurrent
ingests observed causing 5 duplicate ~5×55-call passes). Single-flight collapses
them into one refresh; the rest wait for the winner's result.
"""

import threading
import time
from types import SimpleNamespace
from unittest.mock import MagicMock, patch


def _stub_qdrant_client(refresh_call_counter, delay_seconds=0.5):
    """A Qdrant client stub that counts get_collections() calls and sleeps.

    The delay creates a window where concurrent callers would race without
    single-flight; the counter proves only one made it through.
    """
    stub = MagicMock()

    def get_collections():
        refresh_call_counter["count"] += 1
        time.sleep(delay_seconds)
        return SimpleNamespace(collections=[SimpleNamespace(name="c1"), SimpleNamespace(name="c2")])

    def get_collection(name):
        # Metadata is irrelevant to the test — just return a minimal object.
        return SimpleNamespace(
            config=SimpleNamespace(metadata={}),
            points_count=0,
        )

    stub.get_collections.side_effect = get_collections
    stub.get_collection.side_effect = get_collection
    return stub


def test_singleflight_collapses_concurrent_misses():
    """N concurrent MISSes should trigger exactly one refresh."""
    # Import inside the test so any module-level state is fresh.
    from rag.qdrant import client as client_mod
    from rag.core.cache import get_collection_list_cache
    from rag.core.metadata_cache import get_metadata_cache

    # Reset caches so we're guaranteed a MISS.
    get_collection_list_cache().clear()
    get_metadata_cache()._metadata_by_collection = {}

    # Reset single-flight state in case a prior test left it populated.
    with client_mod._singleflight_lock:
        client_mod._singleflight_event = None

    refresh_counter = {"count": 0}
    stub = _stub_qdrant_client(refresh_counter, delay_seconds=0.5)

    with patch.object(client_mod, "get_qdrant_client", return_value=stub):
        results = []
        threads = []

        def worker():
            results.append(client_mod.list_collections_optimized())

        # Start 5 concurrent callers before the winner finishes its 0.5s refresh.
        for _ in range(5):
            t = threading.Thread(target=worker)
            t.start()
            threads.append(t)

        for t in threads:
            t.join(timeout=5)

    # Assertions:
    #   - only ONE get_collections() call (single-flight held)
    #   - all 5 threads returned the same non-empty list
    assert refresh_counter["count"] == 1, f"expected 1 refresh, got {refresh_counter['count']}"
    assert len(results) == 5
    for r in results:
        assert r is not None
        names = sorted(c.name for c in r)
        assert names == ["c1", "c2"]


def test_singleflight_state_resets_after_refresh():
    """After a refresh completes, the next MISS should proceed cleanly."""
    from rag.qdrant import client as client_mod
    from rag.core.cache import get_collection_list_cache

    # First refresh
    get_collection_list_cache().clear()
    with client_mod._singleflight_lock:
        client_mod._singleflight_event = None

    counter = {"count": 0}
    stub = _stub_qdrant_client(counter, delay_seconds=0.01)

    with patch.object(client_mod, "get_qdrant_client", return_value=stub):
        client_mod.list_collections_optimized()

    assert counter["count"] == 1
    assert client_mod._singleflight_event is None, "in-flight marker not cleared after refresh"

    # Second refresh after a clear should proceed independently.
    get_collection_list_cache().clear()
    with patch.object(client_mod, "get_qdrant_client", return_value=stub):
        client_mod.list_collections_optimized()

    assert counter["count"] == 2
    assert client_mod._singleflight_event is None


def test_singleflight_hit_bypasses_lock():
    """A cache HIT should not touch the single-flight coordination at all."""
    from rag.qdrant import client as client_mod
    from rag.core.cache import get_collection_list_cache

    # Prime the cache with a valid entry so subsequent get() returns it.
    get_collection_list_cache().set([SimpleNamespace(name="cached")])

    # Sabotage the client so a call would blow up; if it's touched, we notice.
    def boom():
        raise AssertionError("cache HIT should never reach Qdrant")

    stub = MagicMock()
    stub.get_collections.side_effect = boom

    with patch.object(client_mod, "get_qdrant_client", return_value=stub):
        result = client_mod.list_collections_optimized()

    assert result is not None
    assert [c.name for c in result] == ["cached"]
