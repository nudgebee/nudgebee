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

    def set(self, collections: List[Any]) -> None:
        """
        Set/refresh the collection list cache.

        Args:
            collections: List of CollectionInfo objects
        """
        with self._lock:
            self._collections = collections
            self._timestamp = time.time()
            logger.info(f"Collection list cache refreshed: {len(collections)} collections")

    def add(self, collection_info) -> None:
        """Add a new collection to the cache without invalidating."""
        with self._lock:
            if self._collections is not None:
                self._collections.append(collection_info)
                self._timestamp = time.time()
                logger.info(
                    f"Collection list cache updated: added '{collection_info.name}' ({len(self._collections)} total)"
                )
            else:
                self._collections = [collection_info]
                self._timestamp = time.time()
                logger.info(f"Collection list cache initialized with '{collection_info.name}'")

    def clear(self) -> None:
        """Clear the entire cache."""
        with self._lock:
            self._collections = None
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
