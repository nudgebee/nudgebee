"""Phase 2: the collection list shared through Redis.

Two things are being proved. A process that starts cold adopts another's
refresh instead of re-reading every collection from Qdrant. And when several
processes miss at once, only one of them actually talks to Qdrant.

The generation/stale-merge logic is deliberately untouched by all this --
test_cache.py still owns it -- because the fetch it reconciles against is
per-process.
"""

import time
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import pytest

from rag.core import cache_backend as cb
from rag.core.cache import CollectionListCache, _SHARED_SNAPSHOT_KEY
from utils.config import Config


@pytest.fixture(autouse=True)
def _memory_backend():
    cb.reset_cache_backend()
    with patch.object(Config, "cache_provider", "in_memory"):
        yield
    cb.reset_cache_backend()


def _info(name, module="events", points=1):
    return SimpleNamespace(name=name, metadata={"module": module}, points_count=points)


# --- warm start --------------------------------------------------------------


def test_a_cold_cache_adopts_another_processes_snapshot():
    """The restart case: no local state, but the shared snapshot is fresh."""
    writer = CollectionListCache(ttl_seconds=1800)
    writer.set([_info("a"), _info("b")], fetched_at_generation=writer.begin_refresh())

    reader = CollectionListCache(ttl_seconds=1800)  # fresh process
    adopted = reader.get()

    assert adopted is not None, "cold cache should have adopted the shared snapshot"
    assert sorted(c.name for c in adopted) == ["a", "b"]
    assert reader.stats()["total_collections"] == 2


def test_a_stale_snapshot_is_not_adopted():
    writer = CollectionListCache(ttl_seconds=1800)
    writer.set([_info("a")], fetched_at_generation=writer.begin_refresh())

    # Age the published snapshot past the reader's TTL.
    payload = cb.get_cache_backend().get(_SHARED_SNAPSHOT_KEY)
    payload["timestamp"] = time.time() - 10_000
    cb.get_cache_backend().set(_SHARED_SNAPSHOT_KEY, payload)

    assert CollectionListCache(ttl_seconds=1800).get() is None


def test_clear_removes_the_shared_snapshot():
    writer = CollectionListCache(ttl_seconds=1800)
    writer.set([_info("a")], fetched_at_generation=writer.begin_refresh())
    assert cb.get_cache_backend().get(_SHARED_SNAPSHOT_KEY) is not None

    writer.clear()
    assert cb.get_cache_backend().get(_SHARED_SNAPSHOT_KEY) is None
    assert CollectionListCache(ttl_seconds=1800).get() is None


def test_add_is_published_so_other_processes_see_it():
    writer = CollectionListCache(ttl_seconds=1800)
    writer.set([_info("a")], fetched_at_generation=writer.begin_refresh())
    writer.add(_info("late"))

    adopted = CollectionListCache(ttl_seconds=1800).get()
    assert sorted(c.name for c in adopted) == ["a", "late"]


def test_local_data_survives_an_unusable_backend():
    """A cache that cannot publish must still work as it always did."""
    broken = MagicMock()
    broken.get.side_effect = RuntimeError("boom")
    broken.set.side_effect = RuntimeError("boom")
    broken.delete.side_effect = RuntimeError("boom")

    with patch("rag.core.cache.get_cache_backend", return_value=broken):
        c = CollectionListCache(ttl_seconds=1800)
        c.set([_info("a")], fetched_at_generation=c.begin_refresh())
        assert [x.name for x in c.get()] == ["a"]


# --- cross-process single-flight --------------------------------------------


def test_only_one_process_refreshes_when_several_miss():
    """The second process must use the first's result, not re-fetch."""
    from rag.qdrant import client as client_mod

    backend = cb.get_cache_backend()
    token = backend.try_lock(client_mod._REFRESH_LOCK_KEY, 60)
    assert token is not None, "first process takes the lock"
    assert backend.try_lock(client_mod._REFRESH_LOCK_KEY, 60) is None, "second is turned away"

    backend.unlock(client_mod._REFRESH_LOCK_KEY, token)
    assert backend.try_lock(client_mod._REFRESH_LOCK_KEY, 60) is not None, "released, so available"


def test_a_lock_is_only_released_by_its_holder():
    """A lock that expired and was retaken must not be freed by the old holder."""
    backend = cb.get_cache_backend()
    mine = backend.try_lock("k", 60)
    backend.unlock("k", "someone-elses-token")
    assert backend.try_lock("k", 60) is None, "still held"
    backend.unlock("k", mine)
    assert backend.try_lock("k", 60) is not None


def test_waiter_adopts_the_snapshot_instead_of_refetching():
    """End to end: lock held elsewhere, snapshot lands, waiter uses it."""
    from rag.qdrant import client as client_mod

    with client_mod._singleflight_lock:
        client_mod._singleflight_event = None

    backend = cb.get_cache_backend()
    holder = backend.try_lock(client_mod._REFRESH_LOCK_KEY, 60)
    assert holder is not None

    # The "other process" publishes its refresh.
    CollectionListCache(ttl_seconds=1800).set([_info("from-other-process")], fetched_at_generation=0)

    stub = MagicMock()
    stub.get_collections.side_effect = AssertionError("must not refetch; snapshot was available")

    with patch.object(client_mod, "get_qdrant_client", return_value=stub):
        with patch.object(client_mod, "_REFRESH_LOCK_WAIT_SECONDS", 3):
            result = client_mod.list_collections_optimized()

    assert [c.name for c in result] == ["from-other-process"]
    stub.get_collections.assert_not_called()
    backend.unlock(client_mod._REFRESH_LOCK_KEY, holder)


def test_adopting_a_snapshot_also_warms_the_local_metadata_tier():
    """Found live: pod B adopted the list, then 404'd when Redis died.

    A process that adopts a snapshot never runs update_from_collections, so
    without this its metadata exists only in the shared tier. The moment that
    tier goes away it answers "not found" for collections that plainly exist
    -- a wrong answer, not a slow one.
    """
    from rag.core.metadata_cache import get_metadata_cache

    writer = CollectionListCache(ttl_seconds=1800)
    writer.set([_info("shared-a"), _info("shared-b")], fetched_at_generation=writer.begin_refresh())

    get_metadata_cache().invalidate()  # the adopting process knows nothing yet
    assert get_metadata_cache().get("shared-a") is None

    assert CollectionListCache(ttl_seconds=1800).get() is not None  # adopt

    cached = get_metadata_cache().get("shared-a")
    assert cached is not None, "adopting must populate the metadata cache"
    assert cached["metadata"] == {"module": "events"}
    assert cached["points_count"] == 1


def test_an_unreadable_snapshot_is_a_miss_not_a_crash():
    """Redis can hold anything: an older build's schema, or a partial write.

    Before this, a snapshot entry missing "name" raised KeyError out of
    cache.get() -- straight into whoever was serving the request.
    """
    backend = cb.get_cache_backend()
    writer = CollectionListCache(ttl_seconds=1800)

    for broken in (
        {"timestamp": time.time(), "collections": [{"no_name_key": 1}]},
        {"timestamp": time.time(), "collections": "not-a-list"},
        {"timestamp": "not-a-number", "collections": []},
        {"collections": [{"name": "a"}]},  # no timestamp
    ):
        backend.set(_SHARED_SNAPSHOT_KEY, broken)
        assert CollectionListCache(ttl_seconds=1800).get() is None, broken

    # A good payload after a bad one still works.
    writer.set([_info("fine")], fetched_at_generation=writer.begin_refresh())
    adopted = CollectionListCache(ttl_seconds=1800).get()
    assert [c.name for c in adopted] == ["fine"]


def test_adopting_does_not_write_the_snapshot_back_to_the_shared_tier():
    """It came from there; echoing 300+ keys back is a wasted round trip."""
    writer = CollectionListCache(ttl_seconds=1800)
    writer.set([_info("a"), _info("b")], fetched_at_generation=writer.begin_refresh())

    backend = cb.get_cache_backend()
    with patch.object(backend, "set_many", wraps=backend.set_many) as spy:
        assert CollectionListCache(ttl_seconds=1800).get() is not None
        assert spy.call_count == 1
        assert spy.call_args.kwargs.get("local_only") is True


def test_the_snapshot_is_taken_under_the_lock():
    """_snapshot_payload used to read self._collections with no lock held.

    Publishing happens after each mutating method releases the lock, so a
    concurrent clear() -- which sets _collections to None -- could land
    between the None check and the iteration and raise TypeError out of
    whichever caller triggered the publish.

    Asserted as an invariant rather than by racing: the window is two
    bytecodes wide, so a timing test passes with or without the fix, which
    makes it worse than no test at all.
    """
    c = CollectionListCache(ttl_seconds=1800)
    c.set([_info("a")], fetched_at_generation=0)

    real_lock = c._lock
    held_during_read = []

    class WatchedLock:
        def __enter__(self):
            real_lock.acquire()
            held_during_read.append("acquired")
            return self

        def __exit__(self, *exc):
            real_lock.release()
            return False

    c._lock = WatchedLock()
    try:
        payload = c._snapshot_payload()
    finally:
        c._lock = real_lock

    assert held_during_read, "_snapshot_payload must read the list under the lock"
    assert [e["name"] for e in payload["collections"]] == ["a"]


def test_timing_out_retries_the_lock_before_refreshing():
    """Without this, replicas that all time out all refresh at once.

    That is precisely the stampede the shared lock exists to prevent, so the
    timeout path has to try once more rather than falling straight through.
    """
    from rag.core.cache import get_collection_list_cache
    from rag.qdrant import client as client_mod

    with client_mod._singleflight_lock:
        client_mod._singleflight_event = None
    # The module-level cache is a singleton and survives the backend reset, so
    # a warm one from an earlier test would short-circuit before the lock path.
    get_collection_list_cache().clear()

    backend = cb.get_cache_backend()
    # Holder takes the lock and never publishes, so the wait will time out.
    holder = backend.try_lock(client_mod._REFRESH_LOCK_KEY, 60)
    assert holder is not None

    stub = MagicMock()
    stub.get_collections.return_value = SimpleNamespace(collections=[SimpleNamespace(name="x")])
    stub.get_collection.return_value = SimpleNamespace(config=SimpleNamespace(metadata={}), points_count=0)

    attempts = []
    real_try_lock = backend.try_lock

    def counting_try_lock(key, ttl):
        token = real_try_lock(key, ttl)
        attempts.append(token)
        return token

    with patch.object(backend, "try_lock", side_effect=counting_try_lock):
        with patch.object(client_mod, "get_qdrant_client", return_value=stub):
            with patch.object(client_mod, "_REFRESH_LOCK_WAIT_SECONDS", 1):
                result = client_mod.list_collections_optimized()

    assert [c.name for c in result] == ["x"], "still returns a result"
    assert len(attempts) == 2, "lock attempted once up front and once after the timeout"
    assert attempts == [None, None], "both refused while the holder keeps it"

    backend.unlock(client_mod._REFRESH_LOCK_KEY, holder)


def test_a_one_collection_list_is_not_shared():
    """A pod that only knows one collection must not hand that to the others.

    add() normally appends to a list a refresh just populated. Reaching it
    with nothing cached means a concurrent invalidate() -- from a delete --
    landed in between, so this process knows one collection and nothing of
    the other few hundred. Publishing that looks fresh to every replica and
    overwrites the complete list in Redis.
    """
    backend = cb.get_cache_backend()
    backend.set(_SHARED_SNAPSHOT_KEY, {"timestamp": time.time(), "collections": [{"name": "real"} for _ in range(3)]})

    c = CollectionListCache(ttl_seconds=60)
    c.add(SimpleNamespace(name="only-one", metadata={}, points_count=0))

    shared = backend.get(_SHARED_SNAPSHOT_KEY)
    assert [e["name"] for e in shared["collections"]] == ["real", "real", "real"], "the good snapshot was overwritten"


def test_a_one_collection_list_is_stale_so_the_next_read_refreshes():
    """A list of one must never be mistaken for the full list.

    Both readers report "unknown" rather than answering from it, which is
    what sends the caller to Qdrant for a real refresh.
    """
    c = CollectionListCache(ttl_seconds=60)
    c.add(SimpleNamespace(name="only-one", metadata={}, points_count=0))

    assert c.get() is None, "the list counts as stale, so the caller refreshes"
    assert c.exists("only-one") is None, "and exists() says unknown, not a confident answer"


def test_the_new_collection_survives_a_refresh_that_was_already_running():
    """Marking it stale must not lose the entry a refresh in flight cannot see."""
    c = CollectionListCache(ttl_seconds=60)
    fetched_at = c.begin_refresh()
    c.add(SimpleNamespace(name="created-mid-fetch", metadata={}, points_count=0))

    # The in-flight fetch lands, without the new collection in its results.
    c.set([SimpleNamespace(name="from-qdrant", metadata={}, points_count=5)], fetched_at_generation=fetched_at)

    assert sorted(x.name for x in c.get()) == ["created-mid-fetch", "from-qdrant"]


def test_appending_to_a_known_list_still_shares_it():
    """The guard must not stop the normal path from publishing."""
    c = CollectionListCache(ttl_seconds=60)
    c.set([SimpleNamespace(name="a", metadata={}, points_count=1)])
    c.add(SimpleNamespace(name="b", metadata={}, points_count=2))

    shared = cb.get_cache_backend().get(_SHARED_SNAPSHOT_KEY)
    assert sorted(e["name"] for e in shared["collections"]) == ["a", "b"]


def test_back_to_back_adds_never_share_a_partial_list():
    """The guard has to survive the second add, not just the first.

    Marking the one-entry list stale is not enough on its own: the next add
    takes the append branch, and if that branch freshens the timestamp and
    publishes, the partial list reaches Redis one call later looking current.
    Back-to-back creates are exactly the re-index path this protects.
    """
    backend = cb.get_cache_backend()
    intact = {"timestamp": time.time(), "collections": [{"name": f"real{i}"} for i in range(324)]}
    backend.set(_SHARED_SNAPSHOT_KEY, intact)

    c = CollectionListCache(ttl_seconds=1800)
    for name in ("first", "second", "third"):
        c.add(SimpleNamespace(name=name, metadata={}, points_count=0))

    shared = backend.get(_SHARED_SNAPSHOT_KEY)
    assert len(shared["collections"]) == 324, "a partial list overwrote the complete one"


def test_a_partial_list_stays_stale_however_many_are_added():
    cb.reset_cache_backend()
    c = CollectionListCache(ttl_seconds=1800)
    for name in ("first", "second", "third"):
        c.add(SimpleNamespace(name=name, metadata={}, points_count=0))

    assert c.get() is None, "still stale, so the caller refreshes"
    assert c.exists("first") is None, "and exists() still says unknown"
    assert cb.get_cache_backend().get(_SHARED_SNAPSHOT_KEY) is None, "nothing was published"


def test_adding_to_an_expired_list_does_not_resurrect_it():
    """An expired list is as incomplete as an empty one.

    Freshening the timestamp on add would hand back a list that is missing
    whatever changed upstream while it sat expired. Asserted on the cache's
    own freshness rather than through get(), which would otherwise adopt the
    shared snapshot and mask the local behaviour.
    """
    c = CollectionListCache(ttl_seconds=1)
    c.set([SimpleNamespace(name="old", metadata={}, points_count=1)])
    expired_at = time.time() - 60
    c._timestamp = expired_at
    cb.reset_cache_backend()  # drop the snapshot set() published, so nothing is adopted

    c.add(SimpleNamespace(name="new", metadata={}, points_count=1))

    assert c._timestamp == expired_at, "add() must not refresh an expired list's clock"
    assert c.get() is None, "so it still reads as needing a refresh"
    assert sorted(x.name for x in c._collections) == ["new", "old"], "but the entry is kept for the merge"
    assert cb.get_cache_backend().get(_SHARED_SNAPSHOT_KEY) is None, "and nothing was published"


def test_the_stale_sentinel_does_not_depend_on_how_far_away_the_epoch_is():
    """0.0 means "never filled", not "filled in 1970".

    Leaving that to the subtraction only works while the wall clock is
    further from the epoch than the TTL. Unreachable in production, but it
    makes the sentinel depend on the calendar, and stats() already tests it
    explicitly.
    """
    backend = cb.get_cache_backend()
    backend.set(_SHARED_SNAPSHOT_KEY, {"timestamp": time.time(), "collections": [{"name": "real"}]})

    c = CollectionListCache(ttl_seconds=1800)
    c.add(SimpleNamespace(name="first", metadata={}, points_count=0))

    # A clock reading well inside the TTL of the epoch.
    with patch("time.time", return_value=100.0):
        c.add(SimpleNamespace(name="second", metadata={}, points_count=0))

    shared = backend.get(_SHARED_SNAPSHOT_KEY)
    assert [e["name"] for e in shared["collections"]] == ["real"], "a partial list was shared"
