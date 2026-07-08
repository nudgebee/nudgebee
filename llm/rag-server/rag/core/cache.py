"""
Collection list cache for RAG server.

Provides fast caching of full collection list with metadata to avoid expensive
Qdrant API calls (N+1 calls to fetch metadata).
"""

import logging
import threading
import time
from typing import Any, Dict, List, Optional

from utils.config import Config

logger = logging.getLogger(__name__)


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

        Returns:
            List of CollectionInfo objects or None if expired
        """
        with self._lock:
            # Check if cache is expired
            if time.time() - self._timestamp > self.ttl_seconds:
                logger.debug("Collection list cache expired, needs refresh")
                return None

            if self._collections is not None:
                logger.debug(f"Collection list cache HIT: {len(self._collections)} collections")
            return self._collections

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
            if self._collections is None:
                self._generation += 1
                self._collections = [collection_info]
                self._collection_generations = {collection_info.name: self._generation}
                self._timestamp = time.time()
                logger.info(f"Collection list cache initialized with '{collection_info.name}'")
                return
            for existing in self._collections:
                if existing.name == collection_info.name:
                    # Concurrent create-if-not-exists path may fire this
                    # from multiple threads after the winner's 200; ignore
                    # without bumping the generation.
                    return
            self._generation += 1
            self._collections.append(collection_info)
            self._collection_generations[collection_info.name] = self._generation
            self._timestamp = time.time()
            logger.info(
                f"Collection list cache updated: added '{collection_info.name}' ({len(self._collections)} total)"
            )

    def clear(self) -> None:
        """Clear the entire cache."""
        with self._lock:
            self._generation += 1
            self._collections = None
            self._collection_generations.clear()
            self._timestamp = 0.0
            logger.info("Collection list cache cleared")

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
