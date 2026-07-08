"""retag_migration must NOT clear the startup-warmed cache when it changed nothing.

The bug we're guarding against: the startup sequence warms the collection list
cache with a slow 44-second N+1 pass. If retag_migration runs immediately after
and calls .invalidate() unconditionally — even when ``migrated=[]`` — the next
ingest (typically minutes later) has to eat that 44-second refresh again. In
steady state (every collection already on the new tag layout, nothing to
migrate) the cache is authoritative and should be left alone.
"""

from types import SimpleNamespace
from unittest.mock import MagicMock, patch


def _seed_cache_with(names):
    from rag.core.cache import get_collection_list_cache

    cache = get_collection_list_cache()
    cache.clear()
    cache.set([SimpleNamespace(name=n, metadata={"module": "already_migrated"}, points_count=0) for n in names])
    return cache


def test_no_migration_leaves_startup_cache_intact():
    """Steady state: every collection skipped, warmed cache must survive."""
    from rag.core.documents import module_retag_migration as rm

    cache = _seed_cache_with(["c1", "c2", "c3"])
    warmed = cache.get()

    # Both imports happen inside the function body — patch at the source module.
    with patch("rag.qdrant.client.list_collections_optimized", return_value=warmed):
        with patch("rag.qdrant.client.get_qdrant_client", return_value=MagicMock()):
            summary = rm.retag_legacy_modules()

    assert summary["migrated"] == [], "test premise: nothing should be migrated"
    assert cache.get() is not None, (
        "cache was invalidated despite migrated=[]; the bug we're fixing. "
        "See module_retag_migration.py: invalidate should be gated on summary['migrated']."
    )
    assert len(cache.get()) == 3


def test_actual_migration_still_invalidates():
    """When migration rewrites at least one collection, cache MUST be invalidated
    (otherwise post-migration reads would return stale CollectionInfo)."""
    from rag.core.documents import module_retag_migration as rm

    # Seed one collection whose metadata.module IS in LEGACY_MODULES, so the
    # migration will attempt to rewrite it. Use a real LEGACY_MODULES entry.
    legacy = next(iter(rm.LEGACY_MODULES))
    cache = _seed_cache_with(["legacy_c"])
    # Overwrite the seeded metadata so it looks legacy.
    cache.clear()
    cache.set([SimpleNamespace(name="legacy_c", metadata={"module": legacy}, points_count=0)])
    warmed = cache.get()

    stub_client = MagicMock()
    # Stub _migrate_single_collection to succeed cheaply.
    with patch("rag.qdrant.client.list_collections_optimized", return_value=warmed):
        with patch("rag.qdrant.client.get_qdrant_client", return_value=stub_client):
            with patch.object(
                rm, "_migrate_single_collection", return_value={"collection": "legacy_c", "old": legacy, "new": "new"}
            ):
                summary = rm.retag_legacy_modules()

    assert len(summary["migrated"]) == 1
    assert cache.get() is None, "cache should be invalidated when migration actually rewrote something"
