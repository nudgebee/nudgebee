"""Tests for CollectionListCache generation-token concurrency fix.

The bug we're guarding against: a slow refresh (list-then-fetch-metadata
across many collections) captures the collection list at time T0, iterates
for tens of seconds, then calls set() with the T0-captured list. If an
add() lands during that iteration, its entry gets silently overwritten
when set() completes. Under concurrent ingest, this makes newly-created
collections invisible to search for the remainder of the refresh window.
"""

from types import SimpleNamespace

from rag.core.cache import CollectionListCache


def _c(name):
    return SimpleNamespace(name=name)


def test_add_bumps_generation():
    cache = CollectionListCache(ttl_seconds=1800)
    g0 = cache.begin_refresh()
    cache.add(_c("a"))
    g1 = cache.begin_refresh()
    assert g1 > g0


def test_clear_bumps_generation():
    cache = CollectionListCache(ttl_seconds=1800)
    cache.add(_c("a"))
    g0 = cache.begin_refresh()
    cache.clear()
    g1 = cache.begin_refresh()
    assert g1 > g0


def test_stale_refresh_preserves_add():
    # The specific race: slow refresh started BEFORE a concurrent add lands.
    cache = CollectionListCache(ttl_seconds=1800)
    cache.set([_c("a"), _c("b")])  # seed with 2 collections
    fetched_at = cache.begin_refresh()  # snapshot pre-fetch generation
    cache.add(_c("new"))  # concurrent ingest creates a new one
    cache.set([_c("a"), _c("b")], fetched_at_generation=fetched_at)  # stale set arrives
    names = {c.name for c in cache.get()}
    assert names == {"a", "b", "new"}, f"stale refresh dropped concurrent add — got {names}"


def test_fresh_refresh_replaces_normally():
    # Non-stale refresh (no add during fetch) should just replace.
    cache = CollectionListCache(ttl_seconds=1800)
    cache.set([_c("a"), _c("b")])
    fetched_at = cache.begin_refresh()
    # No add between begin_refresh() and set() — refresh is fresh.
    cache.set([_c("a"), _c("b"), _c("c")], fetched_at_generation=fetched_at)
    names = {c.name for c in cache.get()}
    assert names == {"a", "b", "c"}


def test_refresh_still_drops_deleted_collections_when_fresh():
    # A collection that Qdrant no longer reports should be dropped, provided
    # no concurrent add ran (fresh refresh).
    cache = CollectionListCache(ttl_seconds=1800)
    cache.set([_c("a"), _c("b"), _c("deleted_upstream")])
    fetched_at = cache.begin_refresh()
    cache.set([_c("a"), _c("b")], fetched_at_generation=fetched_at)
    names = {c.name for c in cache.get()}
    assert names == {"a", "b"}


def test_hard_replace_when_generation_none():
    # Passing fetched_at_generation=None (or omitting) is a hard replace —
    # matches pre-fix behaviour for callers that don't opt into the
    # generation-token pattern.
    cache = CollectionListCache(ttl_seconds=1800)
    cache.set([_c("a"), _c("b")])
    cache.add(_c("new"))
    cache.set([_c("a"), _c("b")])  # no generation → hard replace
    names = {c.name for c in cache.get()}
    assert names == {"a", "b"}


def test_add_is_idempotent():
    # Concurrent create-if-not-exists paths may fire add() for the same
    # name from multiple threads after the winner's 200. Second add is
    # dropped rather than duplicating the entry.
    cache = CollectionListCache(ttl_seconds=1800)
    cache.add(_c("a"))
    cache.add(_c("a"))
    names = [c.name for c in cache.get()]
    assert names == ["a"], f"add() duplicated entry: {names}"


def test_duplicate_add_does_not_resurrect_deleted_collections():
    # Guards against a subtle bug in the generation-token pattern: if add()
    # bumps the generation even when it's a no-op (name already present),
    # a stale refresh completing after that no-op will see its snapshot as
    # stale and merge locally-known-but-fetch-missing entries — resurrecting
    # collections the fetch had legitimately observed as deleted upstream.
    cache = CollectionListCache(ttl_seconds=1800)
    cache.set([_c("a"), _c("b")])
    fetched_at = cache.begin_refresh()  # snapshot BEFORE b is deleted upstream
    cache.add(_c("a"))  # duplicate add — must NOT bump generation
    # Refresh completes after Qdrant deleted b: fetch returns [a] only.
    cache.set([_c("a")], fetched_at_generation=fetched_at)
    names = {c.name for c in cache.get()}
    assert names == {"a"}, f"deleted collection 'b' resurrected via stale-merge: {names}"


def test_stale_merge_does_not_resurrect_when_unrelated_add_lands():
    # The correctness gap Gemini Review round 2 flagged: a *legitimate*
    # concurrent add() during the fetch window will bump the generation
    # counter, so the stale-merge branch fires. Without per-collection
    # generation tracking, the merge loop would re-add ANY locally-known
    # entry missing from the fetch — including entries the fetch legitimately
    # observed as deleted upstream. With per-collection tracking, only the
    # post-snapshot addition survives the merge.
    cache = CollectionListCache(ttl_seconds=1800)
    cache.set([_c("a"), _c("b")])
    fetched_at = cache.begin_refresh()  # snapshot BEFORE upstream deletes b
    cache.add(_c("c"))  # concurrent legitimate add — bumps generation
    # Refresh completes: a still present, b legitimately deleted upstream,
    # c is post-snapshot so the fetch does not see it.
    cache.set([_c("a")], fetched_at_generation=fetched_at)
    names = {c.name for c in cache.get()}
    # c must be preserved (post-fetch add); b must NOT be resurrected.
    assert names == {"a", "c"}, f"expected {{'a','c'}}, got {names}"


def test_set_does_not_mutate_caller_list():
    # set() must not mutate the list passed by the caller — tuples and
    # reused lists must both survive intact.
    cache = CollectionListCache(ttl_seconds=1800)
    cache.set([_c("a")])
    fetched_at = cache.begin_refresh()
    cache.add(_c("mid_refresh"))  # forces the stale-merge branch in set()
    caller_list = [_c("a")]
    caller_list_snapshot = list(caller_list)
    cache.set(caller_list, fetched_at_generation=fetched_at)
    assert [c.name for c in caller_list] == [
        c.name for c in caller_list_snapshot
    ], "set() mutated the caller's list argument"


def test_multiple_refreshes_only_the_first_stale_merges():
    # Two overlapping refreshes: first stale, second fresh. Both must produce
    # a cache that contains the concurrent add.
    cache = CollectionListCache(ttl_seconds=1800)
    cache.set([_c("a")])

    stale_gen = cache.begin_refresh()
    cache.add(_c("concurrent"))  # add lands mid-refresh
    fresh_gen = cache.begin_refresh()  # second refresh starts AFTER

    # First refresh completes (stale) — must merge
    cache.set([_c("a")], fetched_at_generation=stale_gen)
    assert {c.name for c in cache.get()} == {"a", "concurrent"}

    # Second refresh completes (was started after add, so its snapshot
    # sees the current generation — nothing to merge, hard replace fine)
    cache.set([_c("a"), _c("concurrent")], fetched_at_generation=fresh_gen)
    assert {c.name for c in cache.get()} == {"a", "concurrent"}
