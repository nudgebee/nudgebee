"""
Collection list cache for RAG server.

Provides fast caching of full collection list with metadata to avoid expensive
Qdrant API calls (N+1 calls to fetch metadata).
"""

import logging
import threading
import time
from typing import Any, Dict, List, Optional

from rag.core.cache_backend import get_cache_backend
from utils.config import Config

logger = logging.getLogger(__name__)

# One shared snapshot of the whole collection list, published after a refresh.
_SHARED_SNAPSHOT_KEY = "collection_list:snapshot"


class CollectionListCache:
    """
    Cache for full collection list with metadata.

    Caches the complete result from list_collections_optimized()
    to avoid repeated N+1 API calls to Qdrant.

    Thread-safe with TTL-based expiration.
    """

    def __init__(self, ttl_seconds: int = 1800):
        """
        Initialize collection list cache.

        Args:
            ttl_seconds: Cache refresh interval (default: 30 minutes)
        """
        self.ttl_seconds = ttl_seconds
        self._collections: Optional[List[Any]] = None
        self._timestamp = 0.0
        self._lock = threading.RLock()
        # Monotonic generation bumped on every add/clear. A refresh fetch
        # snapshots it via begin_refresh() and passes it back into set() so
        # a slow refresh cannot clobber concurrent additions that landed
        # while it was iterating Qdrant. See test_stale_refresh_preserves_add.
        self._generation = 0
        # Per-collection generation of when each entry was added or last
        # touched. The stale-merge branch of set() consults this to decide
        # whether a locally-known-but-fetch-missing entry was added AFTER
        # the fetch started (preserve) or was already known BEFORE the
        # fetch started (fetch legitimately observed its deletion; drop).
        # See test_stale_merge_does_not_resurrect_when_unrelated_add_lands.
        self._collection_generations: Dict[str, int] = {}

    def get(self) -> Optional[List[Any]]:
        """
        Get cached collection list.

        Returns None if cache is expired and needs refresh.

        Falls back to the shared snapshot before giving up, so a freshly
        started process -- or a second replica -- can start warm instead of
        paying a full Qdrant refresh.

        Returns:
            List of CollectionInfo objects or None if expired
        """
        with self._lock:
            if time.time() - self._timestamp <= self.ttl_seconds and self._collections is not None:
                logger.debug(f"Collection list cache HIT: {len(self._collections)} collections")
                return self._collections

        adopted = self._adopt_shared_snapshot()
        if adopted is not None:
            return adopted

        logger.debug("Collection list cache expired, needs refresh")
        return None

    # --- shared snapshot -----------------------------------------------------
    #
    # The generation/stale-merge logic above stays process-local on purpose:
    # the Qdrant fetch it reconciles against is itself per-process, and that
    # logic is the subtle part. Redis holds only the *result* of a refresh, so
    # another process can skip doing its own.

    def _snapshot_payload(self) -> Optional[Dict[str, Any]]:
        """Serialise the current list, under the lock.

        Publishing happens after each mutating method has released the lock,
        so without taking it here another thread's add() can mutate the list
        mid-iteration -- RuntimeError at best, a half-written snapshot at
        worst. Returns None when nothing is cached, which the caller turns
        into a delete.
        """
        with self._lock:
            if self._collections is None:
                return None
            return {
                "timestamp": self._timestamp,
                "collections": [
                    {"name": c.name, "metadata": c.metadata, "points_count": c.points_count} for c in self._collections
                ],
            }

    def _publish_shared_snapshot(self) -> None:
        """Best-effort. A failure here costs sharing, never correctness."""
        try:
            payload = self._snapshot_payload()
            if payload is None:
                get_cache_backend().delete(_SHARED_SNAPSHOT_KEY)
            else:
                get_cache_backend().set(_SHARED_SNAPSHOT_KEY, payload, ttl_seconds=self.ttl_seconds)
        except Exception:
            logger.warning("Collection list cache: could not publish shared snapshot", exc_info=True)

    def _adopt_shared_snapshot(self) -> Optional[List[Any]]:
        """Adopt another process's refresh if it is still fresh."""
        try:
            payload = get_cache_backend().get(_SHARED_SNAPSHOT_KEY)
        except Exception:
            logger.warning("Collection list cache: could not read shared snapshot", exc_info=True)
            return None

        if not payload or not isinstance(payload, dict):
            return None

        # Imported here: rag.qdrant.client imports this module.
        from rag.qdrant.client import CollectionInfo

        # Anything could be under this key: a payload written by an older or
        # newer build, or a partial write. A snapshot we cannot parse is a
        # cache miss, never an exception thrown at whoever called get().
        try:
            # Parsed in here too: a non-numeric timestamp used to raise
            # straight out of the age check above.
            snapshot_timestamp = float(payload["timestamp"])
            entries = payload["collections"]
            if not isinstance(entries, list):
                raise TypeError(f"collections is {type(entries).__name__}, expected list")
            collections = [
                CollectionInfo(
                    name=entry["name"],
                    metadata=entry.get("metadata") or {},
                    points_count=entry.get("points_count") or 0,
                )
                for entry in entries
            ]
        except Exception:
            logger.warning("Collection list cache: shared snapshot is unreadable, ignoring it", exc_info=True)
            return None

        age = time.time() - snapshot_timestamp
        if age > self.ttl_seconds:
            return None

        with self._lock:
            # A local refresh may have landed while we were reading; it wins.
            if time.time() - self._timestamp <= self.ttl_seconds and self._collections is not None:
                return self._collections
            self._generation += 1
            self._collections = collections
            self._collection_generations = {c.name: self._generation for c in collections}
            self._timestamp = snapshot_timestamp

        # Warm the local metadata tier too. A refreshing process gets this for
        # free via update_from_collections; an adopting one would otherwise
        # hold a warm list and an empty metadata cache, and answer "not found"
        # the moment Redis went away. Caught live with two replicas.
        from rag.core.metadata_cache import get_metadata_cache

        # local_only: these came from the shared tier, so writing all of them
        # straight back to it is a wasted round trip.
        get_metadata_cache().update_from_collections(collections, local_only=True)

        logger.info(
            f"Collection list cache adopted shared snapshot: {len(collections)} collections " f"({age:.0f}s old)"
        )
        return collections

    def begin_refresh(self) -> int:
        """
        Snapshot the current generation before starting a Qdrant list fetch.

        Callers doing a slow list-and-metadata refresh MUST capture the
        generation before the fetch and pass it back to ``set()``. Without
        this, an ``add()`` that lands mid-fetch is silently overwritten
        when the stale fetch result is written back — the bug this cache
        was seeing under concurrent ingest.
        """
        with self._lock:
            return self._generation

    def set(self, collections: List[Any], fetched_at_generation: Optional[int] = None) -> None:
        """
        Set/refresh the collection list cache.

        Args:
            collections: List of CollectionInfo objects returned by the fetch
            fetched_at_generation: Generation snapshot taken via begin_refresh
                before the fetch started. When provided and stale (a local
                add landed during the fetch), locally-known-but-fetch-missing
                entries are merged forward instead of being silently dropped.
                Pass None for a hard replace (rarely correct — prefer clear()).
        """
        with self._lock:
            merged_local = 0
            if fetched_at_generation is not None and fetched_at_generation < self._generation and self._collections:
                # Shallow-copy so we never mutate the caller's list — it may be
                # reused or immutable (a tuple would blow up on .append).
                collections = list(collections)
                fetched_names = {c.name for c in collections}
                for existing in self._collections:
                    if existing.name in fetched_names:
                        continue
                    # Only merge entries that were added AFTER the fetch started —
                    # those are legitimate post-snapshot local additions the fetch
                    # could not have seen. Entries whose own generation predates
                    # the fetch were observable to the fetch; their absence from
                    # the fetch result means they were legitimately deleted
                    # upstream and must NOT be resurrected.
                    if self._collection_generations.get(existing.name, 0) > fetched_at_generation:
                        collections.append(existing)
                        merged_local += 1
            self._collections = collections
            # Rebuild the per-collection generation map so it exactly mirrors
            # the new collections list. Entries we already knew about keep
            # their original generation; entries new to the cache get the
            # current generation as a conservative default.
            self._collection_generations = {
                c.name: self._collection_generations.get(c.name, self._generation) for c in collections
            }
            self._timestamp = time.time()
            if merged_local:
                logger.info(
                    f"Collection list cache refreshed: {len(collections)} collections "
                    f"(merged {merged_local} local additions that landed during fetch)"
                )
            else:
                logger.info(f"Collection list cache refreshed: {len(collections)} collections")
        self._publish_shared_snapshot()

    def add(self, collection_info) -> None:
        """Add a new collection to the cache without invalidating.

        Idempotent — a second add for a name already present is a no-op.
        Only bumps ``_generation`` when the cache is actually modified —
        a no-op duplicate must not bump the counter, otherwise a stale
        refresh completing shortly after would misread its own snapshot
        as stale and resurrect entries that were legitimately deleted
        upstream.
        """
        with self._lock:
            # Whether this process holds the whole picture, decided before the
            # add. Only a complete list may have its freshness extended or be
            # shared; anything else is this process's partial view, and
            # publishing it overwrites the real list in Redis.
            #
            # The timestamp is tested against 0.0 rather than left to the
            # subtraction: 0.0 means "never filled", and reading it as a real
            # instant only works while the epoch is further back than the TTL.
            complete = (
                self._collections is not None
                and self._timestamp > 0.0
                and time.time() - self._timestamp <= self.ttl_seconds
            )

            if self._collections is None:
                # Nothing was cached, so this process knows about exactly one
                # collection and nothing about the other few hundred. The
                # entry is still recorded, with its generation, so a refresh
                # already in flight merges it instead of dropping it. The
                # timestamp stays 0, so get() and exists() report "unknown"
                # and the caller refreshes for real.
                self._generation += 1
                self._collections = [collection_info]
                self._collection_generations = {collection_info.name: self._generation}
                self._timestamp = 0.0
                logger.info(
                    f"Collection list cache initialized with '{collection_info.name}' only; "
                    "marked stale and not shared, the next read will refresh"
                )
            elif any(existing.name == collection_info.name for existing in self._collections):
                # Concurrent create-if-not-exists path may fire this
                # from multiple threads after the winner's 200; ignore
                # without bumping the generation.
                return
            else:
                self._generation += 1
                self._collections.append(collection_info)
                self._collection_generations[collection_info.name] = self._generation
                if complete:
                    self._timestamp = time.time()
                logger.info(
                    f"Collection list cache updated: added '{collection_info.name}' ({len(self._collections)} total)"
                )

            if not complete:
                # Back-to-back creates land here -- the second add appends to
                # the one-entry list above. Freshening and publishing it would
                # put that partial list in Redis looking current, which is the
                # whole failure this guard exists to stop.
                return

        # Publishing is a Redis round trip; it happens with the lock released.
        self._publish_shared_snapshot()

    def clear(self) -> None:
        """Clear the entire cache."""
        with self._lock:
            self._generation += 1
            self._collections = None
            self._collection_generations.clear()
            self._timestamp = 0.0
            logger.info("Collection list cache cleared")
        self._publish_shared_snapshot()

    def invalidate(self) -> None:
        """Invalidate cache (alias for clear)."""
        self.clear()

    def exists(self, collection_name: str) -> Optional[bool]:
        """
        Check if a collection exists in cache (lightweight).

        Returns:
            True if collection exists in cache
            False if collection doesn't exist in cache
            None if cache is expired/empty (needs refresh)
        """
        with self._lock:
            # If cache is expired, return None (unknown)
            if time.time() - self._timestamp > self.ttl_seconds:
                return None

            # If no cache data, return None
            if self._collections is None:
                return None

            # Check if collection name is in cached list
            for collection in self._collections:
                if collection.name == collection_name:
                    return True

            # Collection not found in cache
            return False

    def stats(self) -> Dict[str, Any]:
        """
        Get cache statistics.

        Returns:
            Dict with cache metrics
        """
        with self._lock:
            age_seconds = time.time() - self._timestamp if self._timestamp > 0 else 0
            is_fresh = age_seconds < self.ttl_seconds
            total_collections = len(self._collections) if self._collections else 0

            return {
                "total_collections": total_collections,
                "age_seconds": round(age_seconds, 2),
                "ttl_seconds": self.ttl_seconds,
                "is_fresh": is_fresh,
                "is_cached": self._collections is not None,
            }


# Global collection list cache instance
_collection_list_cache = CollectionListCache(ttl_seconds=Config.rag_collection_cache_ttl)


def get_collection_list_cache() -> CollectionListCache:
    """Get global collection list cache instance."""
    return _collection_list_cache
