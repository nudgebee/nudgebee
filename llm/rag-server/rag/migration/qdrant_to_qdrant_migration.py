"""
Qdrant to Qdrant Migration Service.

Migrates data from embedded Qdrant to standalone Qdrant server.
"""

import gc
import logging
import os
import time
from typing import Dict, Any, Optional

from qdrant_client import QdrantClient
from qdrant_client.models import Distance, VectorParams, PointStruct, HnswConfigDiff

from utils.config import FileConfig

logger = logging.getLogger(__name__)


class QdrantToQdrantMigration:
    """Migrates data from embedded Qdrant to standalone Qdrant server."""

    def __init__(self, source_path: Optional[str] = None, target_url: Optional[str] = None):
        """
        Initialize migration service.

        Args:
            source_path: Path to embedded Qdrant storage (default: from FileConfig)
            target_url: URL of standalone Qdrant server (default: from QDRANT_URL env)
        """
        self.source_path = source_path or FileConfig.qdrant_persistent_path
        self.target_url = target_url or os.getenv("QDRANT_URL")

        if not self.target_url:
            raise ValueError("QDRANT_URL environment variable must be set for standalone Qdrant migration")

        logger.info(f"Initializing Qdrant→Qdrant migration: {self.source_path} → {self.target_url}")

        # Source: Embedded Qdrant
        self.source_client = QdrantClient(path=self.source_path, timeout=300)
        logger.info(f"Connected to embedded Qdrant at: {self.source_path}")

        # Target: Standalone Qdrant with extended timeout for large upserts
        self.target_client = QdrantClient(url=self.target_url, timeout=300)
        logger.info(f"Connected to standalone Qdrant at: {self.target_url}")

    def get_source_stats(self) -> Dict[str, Any]:
        """Get statistics from embedded Qdrant."""
        try:
            collections = self.source_client.get_collections()
            stats: Dict[str, Any] = {"total_collections": len(collections.collections), "collections": []}

            for collection in collections.collections:
                info = self.source_client.get_collection(collection.name)
                stats["collections"].append({"name": collection.name, "points_count": info.points_count or 0})

            return stats
        except Exception as e:
            logger.error(f"Error getting source stats: {e}")
            return {"total_collections": 0, "collections": [], "error": str(e)}

    def get_target_stats(self) -> Dict[str, Any]:
        """Get statistics from standalone Qdrant."""
        try:
            collections = self.target_client.get_collections()
            stats: Dict[str, Any] = {"total_collections": len(collections.collections), "collections": []}

            for collection in collections.collections:
                info = self.target_client.get_collection(collection.name)
                stats["collections"].append({"name": collection.name, "points_count": info.points_count or 0})

            return stats
        except Exception as e:
            logger.error(f"Error getting target stats: {e}")
            return {"total_collections": 0, "collections": [], "error": str(e)}

    def migrate_collection(self, collection_name: str, batch_size: int = 25, force: bool = False) -> Dict[str, Any]:
        """
        Migrate a single collection from embedded to standalone Qdrant.

        Args:
            collection_name: Name of collection to migrate
            batch_size: Batch size for data transfer
            force: If True, delete existing collection and re-migrate

        Returns:
            Migration result with status and stats
        """
        result: Dict[str, Any] = {
            "collection": collection_name,
            "status": "unknown",
            "points_migrated": 0,
            "error": None,
        }

        try:
            logger.info(f"Starting migration of collection: {collection_name}")

            # Get source collection info
            source_info = self.source_client.get_collection(collection_name)
            points_count = source_info.points_count or 0

            logger.info(f"Source collection {collection_name}: {points_count} points")

            # Get vector config
            vector_size = 0
            vector_distance = Distance.COSINE

            if hasattr(source_info.config, "params") and source_info.config.params:
                if hasattr(source_info.config.params, "vectors"):
                    vector_params = source_info.config.params.vectors
                    vector_size = getattr(vector_params, "size", 0)
                    distance_str = str(getattr(vector_params, "distance", "Cosine")).upper()
                    vector_distance = getattr(Distance, distance_str, Distance.COSINE)

            # Get metadata - using native Qdrant API (1.16.0+)
            # Metadata is stored in collection_info.config.metadata
            metadata = {}
            if hasattr(source_info, "config") and hasattr(source_info.config, "metadata"):
                metadata = source_info.config.metadata or {}
            elif hasattr(source_info, "metadata") and source_info.metadata:
                metadata = source_info.metadata

            logger.info(f"Collection {collection_name} metadata: {metadata}")

            # Check if target collection already exists
            target_collections = self.target_client.get_collections()
            target_exists = any(c.name == collection_name for c in target_collections.collections)

            if target_exists:
                if force:
                    logger.info(f"Collection {collection_name} exists in target, force=True, deleting and re-migrating")
                    self.target_client.delete_collection(collection_name)
                else:
                    logger.warning(f"Collection {collection_name} already exists in target, skipping")
                    result["status"] = "already_exists"
                    return result

            # Create collection in target with on_disk=True
            logger.info(f"Creating collection {collection_name} in standalone Qdrant with on_disk=True")
            self.target_client.create_collection(
                collection_name=collection_name,
                vectors_config=VectorParams(
                    size=vector_size, distance=vector_distance, on_disk=True  # Enable on-disk storage
                ),
                hnsw_config=HnswConfigDiff(on_disk=True),  # Enable HNSW on-disk
                metadata=metadata,
            )
            logger.info(f"Created collection {collection_name} with on-disk storage")

            # Migrate data in batches
            offset = None
            migrated_count = 0

            while True:
                # Read batch from source
                records, next_offset = self.source_client.scroll(
                    collection_name=collection_name,
                    limit=batch_size,
                    offset=offset,
                    with_payload=True,
                    with_vectors=True,
                )

                if not records:
                    break

                # Convert Record objects to PointStruct for upsert
                points = [
                    PointStruct(id=record.id, payload=record.payload, vector=record.vector)  # type: ignore[arg-type]
                    for record in records
                ]

                # Write batch to target with retry logic
                max_retries = 3
                retry_count = 0
                while retry_count < max_retries:
                    try:
                        self.target_client.upsert(collection_name=collection_name, points=points)
                        break  # Success, exit retry loop
                    except Exception as e:
                        retry_count += 1
                        if retry_count >= max_retries:
                            logger.error(f"Failed to upsert batch after {max_retries} retries: {e}")
                            raise
                        wait_time = 2**retry_count  # Exponential backoff: 2, 4, 8 seconds
                        logger.warning(
                            f"Upsert failed (attempt {retry_count}/{max_retries}), retrying in {wait_time}s: {e}"
                        )
                        time.sleep(wait_time)

                migrated_count += len(points)
                logger.info(f"Migrated {migrated_count}/{points_count} points from {collection_name}")

                if next_offset is None:
                    break

                offset = next_offset

                # Clean up
                del records, points
                gc.collect()

                # Longer delay between batches to prevent overwhelming the server
                # This prevents health check failures and pod restarts
                time.sleep(2.0)

            # Verify migration
            target_info = self.target_client.get_collection(collection_name)
            target_points = target_info.points_count or 0

            if target_points != points_count:
                logger.warning(
                    f"Point count mismatch for {collection_name}: " f"source={points_count}, target={target_points}"
                )

            result["status"] = "success"
            result["points_migrated"] = migrated_count
            logger.info(f"Successfully migrated collection {collection_name}: {migrated_count} points")

            return result

        except Exception as e:
            logger.error(f"Error migrating collection {collection_name}: {e}", exc_info=True)
            result["status"] = "failed"
            result["error"] = str(e)
            return result

    def migrate_all(self, batch_size: int = 25, force: bool = False) -> Dict[str, Any]:
        """
        Migrate all collections from embedded to standalone Qdrant.

        Uses conservative settings to prevent server overload:
        - Small batch size (default 25)
        - 2 second delay between batches
        - 3 second delay between collections

        Args:
            batch_size: Batch size for data transfer (default: 25, conservative)
            force: If True, re-migrate even if collection exists in target

        Returns:
            Overall migration result
        """
        result: Dict[str, Any] = {
            "total_collections": 0,
            "migrated": 0,
            "skipped": 0,
            "failed": 0,
            "details": [],
            "error": None,
        }

        try:
            # Get source collections
            source_collections = self.source_client.get_collections()
            if not source_collections.collections:
                logger.info("No collections found in embedded Qdrant")
                return result

            result["total_collections"] = len(source_collections.collections)
            logger.info(f"Starting migration of {result['total_collections']} collections")

            for collection in source_collections.collections:
                collection_result = self.migrate_collection(collection.name, batch_size, force)

                if collection_result["status"] == "success":
                    result["migrated"] += 1
                elif collection_result["status"] == "already_exists":
                    result["skipped"] += 1
                else:
                    result["failed"] += 1

                result["details"].append(collection_result)

                # Delay between collections to prevent overwhelming the server
                # This helps avoid health check failures and pod restarts
                time.sleep(3.0)

            logger.info(
                f"Migration complete: {result['migrated']} migrated, "
                f"{result['skipped']} skipped, {result['failed']} failed"
            )

        except Exception as e:
            logger.error(f"Error during migration: {e}", exc_info=True)
            result["error"] = str(e)

        return result

    def validate_migration(self) -> Dict[str, Any]:
        """
        Validate migration prerequisites.

        Returns:
            Validation result
        """
        validation: Dict[str, Any] = {"valid": True, "checks": [], "errors": []}

        try:
            # Check 1: Embedded Qdrant exists and has data
            if not os.path.exists(self.source_path):
                validation["valid"] = False
                validation["errors"].append(f"Embedded Qdrant path not found: {self.source_path}")
            else:
                source_stats = self.get_source_stats()
                validation["checks"].append(
                    {"check": "embedded_qdrant", "status": "ok", "collections": source_stats["total_collections"]}
                )

            # Check 2: Standalone Qdrant is accessible
            try:
                target_stats = self.get_target_stats()
                validation["checks"].append(
                    {"check": "standalone_qdrant", "status": "ok", "collections": target_stats["total_collections"]}
                )
            except Exception as e:
                validation["valid"] = False
                validation["errors"].append(f"Cannot connect to standalone Qdrant: {e}")

            # Check 3: QDRANT_URL is set
            if not self.target_url:
                validation["valid"] = False
                validation["errors"].append("QDRANT_URL environment variable not set")
            else:
                validation["checks"].append({"check": "qdrant_url", "status": "ok", "url": self.target_url})

        except Exception as e:
            validation["valid"] = False
            validation["errors"].append(str(e))

        return validation
