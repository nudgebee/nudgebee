"""
Metadata cache for collections.

Provides O(1) lookups for collection metadata (module, account, points_count)
to eliminate N+1 queries to Qdrant during document processing.

Backed by rag.core.cache_backend, so the same data lives in Redis when one is
configured and in process memory otherwise. Nothing here is secret -- it is
collection names, module/account tags and point counts.
"""

import logging
import threading
from typing import Any, Dict, List, Optional, Set

from rag.core.cache_backend import get_cache_backend

logger = logging.getLogger(__name__)

# Key prefix inside the backend. Also what invalidate() clears wholesale.
_KEY_PREFIX = "collection_metadata:"


class MetadataCache:
    """
    Fast in-memory cache for collection metadata.

    Thread-safe cache that stores essential collection metadata
    (module, account, points_count) to avoid blocking HTTP calls
    to Qdrant during document processing.

    Populated by list_collections_optimized() and used for fast lookups.
    """

    def __init__(self):
        """Initialize metadata cache."""
        # Tracks which keys this process has written, so stats() and a
        # prefix-wide invalidate() work without scanning the backend.
        self._keys: Set[str] = set()
        self._lock = threading.RLock()

    @staticmethod
    def _key(collection_name: str) -> str:
        return _KEY_PREFIX + collection_name

    def get(self, collection_name: str) -> Optional[Dict[str, Any]]:
        """
        Get cached metadata for a collection.

        Args:
            collection_name: Name of the collection

        Returns:
            Dict with 'name', 'metadata', 'points_count' or None if not cached
        """
        cached = get_cache_backend().get(self._key(collection_name))
        if cached:
            logger.debug(f"Metadata cache HIT: {collection_name}")
        else:
            logger.debug(f"Metadata cache MISS: {collection_name}")
        return cached

    def set(self, collection_name: str, metadata: Dict[str, Any], points_count: int = 0) -> None:
        """
        Cache metadata for a single collection.

        Args:
            collection_name: Name of the collection
            metadata: Metadata dictionary
            points_count: Number of points in collection
        """
        get_cache_backend().set(
            self._key(collection_name),
            {"name": collection_name, "metadata": metadata, "points_count": points_count},
        )
        with self._lock:
            self._keys.add(collection_name)
        logger.debug(f"Metadata cached: {collection_name}")

    def update_from_collections(self, collections: List[Any], local_only: bool = False) -> None:
        """
        Bulk update cache from list of CollectionInfo objects.

        This is called by list_collections_optimized() to populate
        the cache with all collection metadata at once.

        Args:
            collections: List of CollectionInfo objects with name, metadata, points_count
            local_only: skip the shared tier -- used when these collections were
                just read from it, so writing them back is a wasted round trip
        """
        # One batched write: a few hundred individual round trips dominated the
        # refresh once the backend was remote.
        get_cache_backend().set_many(
            {
                self._key(coll.name): {
                    "name": coll.name,
                    "metadata": coll.metadata,
                    "points_count": coll.points_count,
                }
                for coll in collections
            },
            local_only=local_only,
        )
        with self._lock:
            self._keys.update(coll.name for coll in collections)
        logger.info(f"Metadata cache bulk updated: {len(collections)} collections")

    def invalidate(self, collection_name: Optional[str] = None) -> None:
        """
        Invalidate cache entry or entire cache.

        Args:
            collection_name: Name of collection to invalidate, or None to clear all
        """
        backend = get_cache_backend()
        if collection_name:
            backend.delete(self._key(collection_name))
            with self._lock:
                self._keys.discard(collection_name)
            logger.info(f"Metadata cache invalidated: {collection_name}")
        else:
            backend.clear(_KEY_PREFIX)
            with self._lock:
                self._keys.clear()
            logger.info("Metadata cache cleared")

    def exists(self, collection_name: str) -> bool:
        """
        Check if collection metadata is cached.

        Args:
            collection_name: Name of the collection

        Returns:
            True if cached, False otherwise
        """
        return get_cache_backend().get(self._key(collection_name)) is not None

    def stats(self) -> Dict[str, Any]:
        """
        Get cache statistics.

        Returns:
            Dict with cache metrics
        """
        with self._lock:
            names = sorted(self._keys)
        return {
            "total_cached": len(names),
            "collection_names": names,
            "backend": get_cache_backend().name,
        }


# Global metadata cache instance
_metadata_cache = MetadataCache()


def get_metadata_cache() -> MetadataCache:
    """Get global metadata cache instance."""
    return _metadata_cache
