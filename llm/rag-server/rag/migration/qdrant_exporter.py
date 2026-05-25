"""
Qdrant Exporter for Migration.

Exports collections, documents, embeddings, and metadata from Qdrant
for migration to another Qdrant instance.
"""

import gc
import logging
from typing import Dict, Any, Optional

from rag.qdrant import get_qdrant_client

logger = logging.getLogger(__name__)


class QdrantExporter:
    """Exports data from Qdrant collections."""

    def __init__(self):
        self.client = get_qdrant_client()

    def export_collection(self, collection_name: str, batch_size: int = 100) -> Optional[Dict[str, Any]]:
        """
        Export a complete collection from Qdrant.

        Args:
            collection_name: Name of the collection to export
            batch_size: Number of points to retrieve per batch

        Returns:
            Dictionary containing collection data or None if collection doesn't exist
        """
        try:
            # Check if collection exists
            collections = self.client.get_collections()
            if not any(c.name == collection_name for c in collections.collections):
                logger.warning(f"Collection {collection_name} not found")
                return None

            # Get collection info
            collection_info = self.client.get_collection(collection_name)
            points_count = collection_info.points_count or 0

            logger.info(f"Exporting collection: {collection_name} ({points_count} points)")

            # Get metadata if exists
            metadata: Dict[str, Any] = {}
            if hasattr(collection_info, "metadata"):
                metadata = collection_info.metadata or {}

            # Export all points
            ids = []
            documents = []
            embeddings = []
            metadatas = []

            offset = None
            exported_count = 0

            while True:
                # Scroll through collection
                points, next_offset = self.client.scroll(
                    collection_name=collection_name,
                    limit=batch_size,
                    offset=offset,
                    with_payload=True,
                    with_vectors=True,
                )

                if not points:
                    break

                # Extract data from points
                for point in points:
                    ids.append(str(point.id))

                    # Document content is in payload
                    payload = point.payload or {}
                    documents.append(payload.get("page_content", ""))

                    # Metadata is in payload (excluding page_content)
                    point_metadata = {k: v for k, v in payload.items() if k != "page_content"}
                    metadatas.append(point_metadata)

                    # Embeddings
                    embeddings.append(point.vector if isinstance(point.vector, list) else list(point.vector))

                exported_count += len(points)
                logger.info(f"Exported {exported_count}/{points_count} points from {collection_name}")

                # Check if we've reached the end
                if next_offset is None:
                    break

                offset = next_offset

                # Safety check
                if exported_count >= points_count * 1.5:
                    logger.warning(f"Exported more points than expected, stopping at {exported_count}")
                    break

                # Clean up batch memory
                del points
                gc.collect()

            logger.info(f"Completed export of {collection_name}: {len(ids)} points")

            return {
                "name": collection_name,
                "metadata": metadata,
                "ids": ids,
                "documents": documents,
                "embeddings": embeddings,
                "metadatas": metadatas,
            }

        except Exception as e:
            logger.error(f"Error exporting collection {collection_name}: {e}", exc_info=True)
            return None
        finally:
            gc.collect()

    def export_all_collections(self, batch_size: int = 100) -> Dict[str, Any]:
        """
        Export all collections from Qdrant.

        Args:
            batch_size: Number of points to retrieve per batch

        Returns:
            Dictionary with all exported collections
        """
        result: Dict[str, Any] = {
            "total_collections": 0,
            "exported_collections": 0,
            "failed_collections": 0,
            "collections": [],
            "errors": [],
        }

        try:
            collections = self.client.get_collections()
            if not collections.collections:
                logger.info("No collections found to export")
                return result

            result["total_collections"] = len(collections.collections)
            logger.info(f"Starting export of {len(collections.collections)} collections")

            for collection in collections.collections:
                try:
                    collection_data = self.export_collection(collection.name, batch_size)

                    if collection_data:
                        result["collections"].append(collection_data)
                        result["exported_collections"] += 1
                        logger.info(f"Successfully exported collection: {collection.name}")
                    else:
                        result["failed_collections"] += 1
                        result["errors"].append({"collection": collection.name, "error": "Export returned None"})

                except Exception as e:
                    logger.error(f"Error exporting collection {collection.name}: {e}")
                    result["failed_collections"] += 1
                    result["errors"].append({"collection": collection.name, "error": str(e)})

            logger.info(
                f"Export complete: {result['exported_collections']}/{result['total_collections']} collections exported"
            )

        except Exception as e:
            logger.error(f"Error during export: {e}", exc_info=True)
            result["error"] = str(e)

        return result
