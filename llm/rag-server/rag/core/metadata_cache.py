"""
Fast in-memory metadata cache for collections.

Provides O(1) lookups for collection metadata (module, account, points_count)
to eliminate N+1 queries to Qdrant during document processing.
"""

import logging
import threading
from typing import Any, Dict, List, Optional

logger = logging.getLogger(__name__)


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
        self._cache: Dict[str, Dict[str, Any]] = {}
        self._lock = threading.RLock()

    def get(self, collection_name: str) -> Optional[Dict[str, Any]]:
        """
        Get cached metadata for a collection.

        Args:
            collection_name: Name of the collection

        Returns:
            Dict with 'name', 'metadata', 'points_count' or None if not cached
        """
        with self._lock:
            cached = self._cache.get(collection_name)
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
        with self._lock:
            self._cache[collection_name] = {
                "name": collection_name,
                "metadata": metadata,
                "points_count": points_count,
            }
            logger.debug(f"Metadata cached: {collection_name}")

    def update_from_collections(self, collections: List[Any]) -> None:
        """
        Bulk update cache from list of CollectionInfo objects.

        This is called by list_collections_optimized() to populate
        the cache with all collection metadata at once.

        Args:
            collections: List of CollectionInfo objects with name, metadata, points_count
        """
        with self._lock:
            for coll in collections:
                self._cache[coll.name] = {
                    "name": coll.name,
                    "metadata": coll.metadata,
                    "points_count": coll.points_count,
                }
            logger.info(f"Metadata cache bulk updated: {len(collections)} collections")

    def invalidate(self, collection_name: Optional[str] = None) -> None:
        """
        Invalidate cache entry or entire cache.

        Args:
            collection_name: Name of collection to invalidate, or None to clear all
        """
        with self._lock:
            if collection_name:
                if collection_name in self._cache:
                    del self._cache[collection_name]
                    logger.info(f"Metadata cache invalidated: {collection_name}")
            else:
                self._cache.clear()
                logger.info("Metadata cache cleared")

    def exists(self, collection_name: str) -> bool:
        """
        Check if collection metadata is cached.

        Args:
            collection_name: Name of the collection

        Returns:
            True if cached, False otherwise
        """
        with self._lock:
            return collection_name in self._cache

    def stats(self) -> Dict[str, Any]:
        """
        Get cache statistics.

        Returns:
            Dict with cache metrics
        """
        with self._lock:
            return {
                "total_cached": len(self._cache),
                "collection_names": list(self._cache.keys()),
            }


# Global metadata cache instance
_metadata_cache = MetadataCache()


def get_metadata_cache() -> MetadataCache:
    """Get global metadata cache instance."""
    return _metadata_cache
