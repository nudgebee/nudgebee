"""
Qdrant Client Manager for RAG Server.

This module provides a thread-safe shared Qdrant client.
Qdrant is thread-safe so we use a single shared client for better performance.
Uses persistent storage for data persistence.
"""

import atexit
import gc
import logging
import os
import threading
import warnings
from typing import Any, Dict, Optional, List, cast

import httpx
from qdrant_client import QdrantClient
from qdrant_client.http.exceptions import ResponseHandlingException
from qdrant_client.http.models import CollectionDescription
from qdrant_client.models import Distance, VectorParams, HnswConfigDiff

from utils.config import FileConfig

# Suppress Qdrant local mode warnings for large collections
# We're aware of the limitation and accept the tradeoff for simplicity
warnings.filterwarnings(
    "ignore", message="Local mode is not recommended for collections with more than", category=UserWarning
)

# Global Qdrant client - single shared instance (Qdrant is thread-safe!)
_qdrant_client: Optional[QdrantClient] = None
_client_lock = threading.RLock()

logger = logging.getLogger(__name__)


def _get_vector_config(collection_info):
    """Extract vector configuration from collection info."""
    vector_size = 0
    vector_distance = Distance.COSINE
    vectors_on_disk = False

    if hasattr(collection_info.config, "params") and collection_info.config.params:
        params = collection_info.config.params
        if hasattr(params, "vectors") and params.vectors:
            vector_params = params.vectors
            vector_size = getattr(vector_params, "size", 0)
            # Get distance as Distance enum
            distance_value = getattr(vector_params, "distance", Distance.COSINE)
            if isinstance(distance_value, str):
                # Convert string to Distance enum
                distance_str = distance_value.upper()
                vector_distance = getattr(Distance, distance_str, Distance.COSINE)
            else:
                vector_distance = distance_value
            on_disk_value = getattr(vector_params, "on_disk", None)
            vectors_on_disk = on_disk_value is True

    return vector_size, vector_distance, vectors_on_disk


def _is_hnsw_on_disk(collection_info):
    """Check if HNSW index is on disk."""
    if collection_info.config.hnsw_config:
        on_disk_value = getattr(collection_info.config.hnsw_config, "on_disk", None)
        return on_disk_value is True
    return False


def _export_collection_points(client: QdrantClient, collection_name: str, points_count: int, scroll_limit: int = 100):
    """Export all points from a collection."""
    logger.info(f"Step 1/4: Exporting {points_count} points from {collection_name}")
    all_points = []
    offset = None

    while True:
        points, next_offset = client.scroll(
            collection_name=collection_name,
            limit=scroll_limit,
            offset=offset,
            with_payload=True,
            with_vectors=True,
        )

        if not points:
            break

        all_points.extend(points)
        logger.debug(f"Exported {len(all_points)}/{points_count} points")

        if next_offset is None:
            break

        offset = next_offset

        # Safety check to prevent infinite loops
        if len(all_points) >= points_count * 1.5:
            logger.warning(f"Exported more points than expected, stopping at {len(all_points)}")
            break

    logger.info(f"Step 1/4: Exported {len(all_points)} points")
    return all_points


def _recreate_collection_with_ondisk(
    client: QdrantClient, collection_name: str, vector_size: int, vector_distance: Distance, metadata: Dict[str, Any]
):
    """Recreate collection with on-disk storage enabled."""
    logger.info(f"Step 2/4: Deleting collection {collection_name}")
    client.delete_collection(collection_name)
    logger.info(f"Step 2/4: Deleted collection {collection_name}")

    logger.info(f"Step 3/4: Recreating {collection_name} with on-disk storage")
    client.create_collection(
        collection_name=collection_name,
        vectors_config=VectorParams(size=vector_size, distance=vector_distance, on_disk=True),
        hnsw_config=HnswConfigDiff(on_disk=True),
        metadata=metadata,
    )
    logger.info(f"Step 3/4: Recreated collection {collection_name}")


def _import_collection_points(client: QdrantClient, collection_name: str, all_points: list, batch_size: int = 100):
    """Import points back to collection in batches."""
    logger.info(f"Step 4/4: Re-importing {len(all_points)} points")

    for i in range(0, len(all_points), batch_size):
        batch = all_points[i : i + batch_size]
        client.upsert(collection_name=collection_name, points=batch)
        logger.debug(f"Re-imported {min(i + batch_size, len(all_points))}/{len(all_points)} points")
        del batch
        gc.collect()

    logger.info(f"Step 4/4: Re-imported all {len(all_points)} points")


def _process_collection(client: QdrantClient, collection_name: str, result: Dict[str, Any]) -> Dict[str, Any]:
    """Process a single collection for on-disk storage configuration."""
    collection_detail: Dict[str, Any] = {
        "collection": collection_name,
        "status": "unknown",
        "on_disk": None,
        "error": None,
    }

    try:
        # Check current configuration
        collection_info = client.get_collection(collection_name)

        # Get vector configuration
        vector_size, vector_distance, vectors_on_disk = _get_vector_config(collection_info)
        hnsw_on_disk = _is_hnsw_on_disk(collection_info)

        # Both vectors and HNSW index must be on disk for full optimization
        current_on_disk = vectors_on_disk and hnsw_on_disk
        logger.info(
            f"Collection {collection_name}: vectors_on_disk={vectors_on_disk}, "
            f"hnsw_on_disk={hnsw_on_disk}, current_on_disk={current_on_disk}"
        )

        if current_on_disk:
            collection_detail["status"] = "already_configured"
            collection_detail["on_disk"] = True
            result["already_configured"] += 1
            logger.debug(f"Collection {collection_name} already has on-disk storage enabled")
        else:
            # Migrate collection to on-disk storage
            points_count = collection_info.points_count or 0
            metadata = getattr(collection_info, "metadata", {}) or {}

            logger.info(f"Migrating collection {collection_name} ({points_count} points) to on-disk storage...")

            # Export, recreate, and import
            all_points = _export_collection_points(client, collection_name, points_count)
            _recreate_collection_with_ondisk(client, collection_name, vector_size, vector_distance, metadata)
            _import_collection_points(client, collection_name, all_points)

            # Verify the count matches
            new_collection_info = client.get_collection(collection_name)
            new_points_count = new_collection_info.points_count or 0

            if new_points_count != points_count:
                logger.warning(
                    f"Point count mismatch for {collection_name}: expected {points_count}, got {new_points_count}"
                )

            collection_detail["status"] = "newly_configured"
            collection_detail["on_disk"] = True
            collection_detail["points_migrated"] = len(all_points)
            result["newly_configured"] += 1
            logger.info(f"Successfully migrated collection {collection_name} to on-disk storage")

    except Exception as e:
        collection_detail["status"] = "failed"
        collection_detail["error"] = str(e)
        result["failed"] += 1
        logger.warning(f"Failed to configure on-disk storage for {collection_name}: {e}")

    return collection_detail


def _configure_ondisk_storage(client: QdrantClient) -> Dict[str, Any]:
    """
    Configure all existing collections to use on-disk storage.
    This significantly reduces RAM usage by keeping vectors on disk.

    NOTE: Since on_disk cannot be updated after collection creation, this function
    recreates collections by exporting data, deleting, recreating with on_disk=True, then re-importing.
    Migration process: export all data → delete collection → recreate with on_disk=True → re-import data

    This manual approach is required because:
    - init_from was removed in qdrant-client v1.16.0
    - Snapshots are not supported in embedded/local Qdrant mode

    See: https://qdrant.tech/documentation/concepts/snapshots/

    WARNING: This causes brief downtime for each collection during recreation.
    Collections are processed one at a time to minimize disruption.

    Returns:
        Dictionary with configuration results and details
    """
    result: Dict[str, Any] = {
        "total_collections": 0,
        "already_configured": 0,
        "newly_configured": 0,
        "failed": 0,
        "details": [],
    }

    try:
        collections = client.get_collections()
        if not collections.collections:
            logger.info("No collections found - skipping on-disk configuration")
            return result

        result["total_collections"] = len(collections.collections)
        logger.info(f"Checking {len(collections.collections)} collections for on-disk storage...")

        for collection in collections.collections:
            collection_detail = _process_collection(client, collection.name, result)
            result["details"].append(collection_detail)

        result["configured"] = result["already_configured"] + result["newly_configured"]
        logger.info(
            f"On-disk storage configuration complete: "
            f"{result['configured']}/{result['total_collections']} configured "
            f"({result['newly_configured']} new, {result['already_configured']} already configured, "
            f"{result['failed']} failed)"
        )

    except Exception as e:
        logger.error(f"Error configuring on-disk storage: {e}")
        result["error"] = str(e)

    return result


def get_qdrant_client() -> QdrantClient:
    """
    Returns a shared Qdrant client instance.

    NOTE: Qdrant is thread-safe so we use a single shared client
    instead of per-thread clients. This is MUCH faster as it avoids repeated
    database initialization.

    SUPPORTS TWO MODES:
    1. Local/Embedded Mode: QDRANT_URL not set - uses local persistent storage
    2. Server Mode: QDRANT_URL set - connects to standalone Qdrant server

    Migration: Set QDRANT_URL=http://qdrant:6333 to use standalone Qdrant server
    """
    global _qdrant_client

    with _client_lock:
        if _qdrant_client is None:
            logger.info("Creating shared Qdrant client (thread-safe)")
            try:
                # Check if server mode is configured
                qdrant_url = os.getenv("QDRANT_URL")

                if qdrant_url:
                    # Server mode - connect to external Qdrant server
                    logger.info(f"Using Qdrant server mode: {qdrant_url}")
                    try:
                        _qdrant_client = QdrantClient(url=qdrant_url)
                        logger.info(f"Connected to Qdrant server at: {qdrant_url}")
                    except (
                        ResponseHandlingException,
                        httpx.RequestError,
                        httpx.TimeoutException,
                        httpx.ConnectError,
                    ) as e:
                        logger.warning(f"Failed to connect to Qdrant server at {qdrant_url}: {e}")
                        logger.info("Qdrant client will retry connection on next request")
                        raise ConnectionError(f"Cannot connect to Qdrant server: {e}") from e
                else:
                    # Local mode - embedded database
                    logger.info("Using Qdrant local mode (embedded)")
                    qdrant_path = FileConfig.qdrant_persistent_path
                    os.makedirs(qdrant_path, exist_ok=True)
                    _qdrant_client = QdrantClient(path=qdrant_path)
                    logger.info(f"Created shared Qdrant client with persistent storage at: {qdrant_path}")

                logger.info(
                    "Qdrant client created successfully. "
                    "Use /admin/configure-ondisk endpoint to enable on-disk storage."
                )

            except Exception as e:
                logger.error(f"Failed to create Qdrant client: {e}")
                raise
        return _qdrant_client


def close_all_clients():
    """Close the shared Qdrant client."""
    global _qdrant_client

    with _client_lock:
        if _qdrant_client is not None:
            logger.info("Closing shared Qdrant client")
            try:
                _qdrant_client.close()
                _qdrant_client = None
            except Exception as e:
                logger.warning(f"Error closing Qdrant client: {e}")
    gc.collect()


# Register shutdown handler
atexit.register(close_all_clients)


class CollectionInfo:
    """
    Collection information with metadata.

    This is a simple wrapper to provide collection info with metadata
    since CollectionDescription from get_collections() doesn't include metadata.
    """

    def __init__(self, name: str, metadata: Optional[Dict[str, Any]] = None, points_count: int = 0):
        self.name = name
        self.metadata = metadata or {}
        self.points_count = points_count


def list_collections_optimized() -> List[CollectionInfo]:
    """
    List all collections with metadata (cached).

    Uses a 30-minute TTL cache to avoid repeated N+1 API calls to Qdrant.
    Qdrant's get_collections() doesn't return metadata, so we need to
    call get_collection() for each collection to get metadata.

    Returns:
        List of CollectionInfo objects with name, metadata, and points_count
    """
    from rag.core.cache import get_collection_list_cache
    from rag.core.metadata_cache import get_metadata_cache

    # Check cache first
    cache = get_collection_list_cache()
    cached_collections = cache.get()
    if cached_collections is not None:
        return cached_collections

    # Cache miss - fetch from Qdrant
    logger.info("Collection list cache MISS - fetching from Qdrant")
    client = get_qdrant_client()

    try:
        # First, get all collection names
        response = client.get_collections()
        collection_descriptions: List[CollectionDescription] = (
            response.collections if hasattr(response, "collections") else cast(List[CollectionDescription], response)
        )

        # Then fetch metadata for each collection
        collections_with_metadata = []
        for coll_desc in collection_descriptions:
            try:
                # Get detailed collection info including metadata
                coll_info = client.get_collection(coll_desc.name)

                # Extract metadata from config
                metadata = {}
                if hasattr(coll_info, "config") and hasattr(coll_info.config, "metadata"):
                    metadata = coll_info.config.metadata or {}

                # Get points count
                points_count = getattr(coll_info, "points_count", 0)

                collections_with_metadata.append(
                    CollectionInfo(name=coll_desc.name, metadata=metadata, points_count=points_count)
                )
            except Exception as e:
                logger.warning(f"Error fetching metadata for collection {coll_desc.name}: {e}")
                # Add collection with empty metadata on error
                collections_with_metadata.append(CollectionInfo(name=coll_desc.name, metadata={}, points_count=0))

        logger.debug(f"Fetched {len(collections_with_metadata)} collections with metadata from Qdrant")

        # Update both caches
        cache.set(collections_with_metadata)
        get_metadata_cache().update_from_collections(collections_with_metadata)

        return collections_with_metadata

    except Exception as e:
        logger.error(f"Error listing collections: {e}")
        return []
