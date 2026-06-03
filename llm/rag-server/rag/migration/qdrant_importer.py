"""
Qdrant Importer for Migration.

Imports collections, documents, embeddings, and metadata from export data
into Qdrant with proper configuration.
"""

import gc
import logging
from typing import Dict, List, Any, Optional

from qdrant_client.models import Distance, VectorParams, PointStruct, HnswConfigDiff

from rag.qdrant import get_qdrant_client

logger = logging.getLogger(__name__)


class QdrantImporter:
    """Imports data into Qdrant collections."""

    def __init__(self):
        self.client = get_qdrant_client()

    def collection_exists(self, collection_name: str) -> bool:
        """
        Check if a collection exists in Qdrant.

        Args:
            collection_name: Name of the collection

        Returns:
            True if collection exists, False otherwise
        """
        try:
            collections = self.client.get_collections()
            return any(c.name == collection_name for c in collections.collections)
        except Exception as e:
            logger.error(f"Error checking collection existence: {e}")
            return False

    def create_collection(
        self, collection_name: str, vector_size: int, metadata: Optional[Dict[str, Any]] = None
    ) -> bool:
        """
        Create a new Qdrant collection with native metadata support.

        Args:
            collection_name: Name of the collection
            vector_size: Dimension of the embedding vectors
            metadata: Optional metadata to store with the collection

        Returns:
            True if successful, False otherwise
        """
        try:
            if self.collection_exists(collection_name):
                logger.warning(f"Collection {collection_name} already exists, skipping creation")
                return True

            # Create collection with cosine distance
            # Qdrant 1.16.0+ supports native metadata
            # Configure on-disk storage for BOTH vectors and HNSW index from the start
            # This provides high precision with low memory footprint
            # See: https://qdrant.tech/documentation/guides/optimize/#2-high-precision-with-low-memory-usage
            self.client.create_collection(
                collection_name=collection_name,
                vectors_config=VectorParams(
                    size=vector_size, distance=Distance.COSINE, on_disk=True  # Store vectors on disk
                ),
                metadata=metadata or {},  # Native metadata support!
                hnsw_config=HnswConfigDiff(on_disk=True),  # Store HNSW index on disk
            )

            logger.info(
                f"Created collection: {collection_name} with vector size: {vector_size} "
                f"(on-disk storage enabled for vectors and HNSW index)"
            )
            if metadata:
                logger.debug(f"Stored metadata for {collection_name}: {metadata}")

            return True

        except Exception as e:
            logger.error(f"Error creating collection {collection_name}: {e}", exc_info=True)
            return False

    def import_documents(
        self,
        collection_name: str,
        documents: List[str],
        embeddings: List[List[float]],
        metadatas: List[Dict[str, Any]],
        ids: List[str],
        batch_size: int = 100,
    ) -> int:
        """
        Import documents with embeddings into Qdrant.

        Args:
            collection_name: Name of the target collection
            documents: List of document texts
            embeddings: List of embedding vectors
            metadatas: List of metadata dictionaries
            ids: List of document IDs
            batch_size: Number of points to upload per batch

        Returns:
            Number of documents successfully imported
        """
        if len(documents) == 0 or len(embeddings) == 0 or len(ids) == 0:
            logger.warning(f"No documents to import for collection {collection_name}")
            return 0

        try:
            total_docs = len(documents)
            imported_count = 0

            logger.info(f"Importing {total_docs} documents into collection {collection_name}")

            # Process in batches
            for i in range(0, total_docs, batch_size):
                batch_end = min(i + batch_size, total_docs)

                # Create point structures for this batch
                points = []
                for j in range(i, batch_end):
                    # Combine document text and metadata into payload
                    # Handle None metadata values
                    metadata = metadatas[j] if j < len(metadatas) else None
                    payload = {
                        "page_content": documents[j],
                        **(metadata if metadata is not None else {}),
                    }

                    point = PointStruct(id=ids[j], vector=embeddings[j], payload=payload)
                    points.append(point)

                # Upload batch
                self.client.upsert(collection_name=collection_name, points=points)

                imported_count += len(points)
                logger.info(
                    f"Imported batch {i // batch_size + 1}: {imported_count}/{total_docs} documents "
                    f"({(imported_count / total_docs) * 100:.1f}%)"
                )

                # Clean up batch memory
                del points
                gc.collect()

            logger.info(f"Successfully imported {imported_count} documents into {collection_name}")
            return imported_count

        except Exception as e:
            logger.error(f"Error importing documents into {collection_name}: {e}", exc_info=True)
            return 0

    def import_collection(self, collection_data: Dict[str, Any], batch_size: int = 100) -> bool:
        """
        Import a complete collection from export data.

        Args:
            collection_data: Dictionary containing collection metadata and documents
            batch_size: Number of documents to import per batch

        Returns:
            True if successful, False otherwise
        """
        try:
            collection_name = collection_data.get("name")
            if not collection_name:
                logger.error("Collection data missing 'name' field")
                return False

            logger.info(f"Starting import of collection: {collection_name}")

            # Handle batched export format
            if "batches" in collection_data:
                return self._import_batched_collection(collection_data, batch_size)

            # Get data from collection export
            documents = collection_data.get("documents", [])
            embeddings = collection_data.get("embeddings", [])
            metadatas = collection_data.get("metadatas", [])
            ids = collection_data.get("ids", [])
            metadata = collection_data.get("metadata", {})

            if len(embeddings) == 0:
                logger.warning(f"No embeddings found for collection {collection_name}, skipping")
                return False

            # Determine vector size from first embedding
            vector_size = len(embeddings[0]) if len(embeddings) > 0 else 0

            # Create collection
            if not self.create_collection(collection_name, vector_size, metadata):
                return False

            # Import documents
            imported_count = self.import_documents(collection_name, documents, embeddings, metadatas, ids, batch_size)

            success = imported_count == len(documents)
            if success:
                logger.info(f"Successfully imported collection {collection_name}")
            else:
                logger.warning(
                    f"Partial import for collection {collection_name}: " f"{imported_count}/{len(documents)} documents"
                )

            return success

        except Exception as e:
            logger.error(f"Error importing collection: {e}", exc_info=True)
            return False
        finally:
            gc.collect()

    def _import_batched_collection(self, collection_data: Dict[str, Any], batch_size: int = 100) -> bool:
        """
        Import a collection that was exported in batches.

        Args:
            collection_data: Dictionary containing batched collection data
            batch_size: Number of documents to import per batch

        Returns:
            True if successful, False otherwise
        """
        try:
            collection_name = collection_data.get("name")
            if not collection_name or not isinstance(collection_name, str):
                logger.error("Invalid or missing collection name in batched collection data")
                return False

            batches = collection_data.get("batches", [])
            metadata = collection_data.get("metadata", {})

            if len(batches) == 0:
                logger.warning(f"No batches found for collection {collection_name}")
                return False

            # Get vector size from first batch's first embedding
            first_batch = batches[0]
            first_embeddings = first_batch.get("embeddings", [])
            vector_size = len(first_embeddings[0]) if len(first_embeddings) > 0 else 0

            # Create collection
            if not self.create_collection(collection_name, vector_size, metadata):
                return False

            # Import each batch
            total_imported = 0
            for batch_idx, batch in enumerate(batches, 1):
                logger.info(f"Importing batch {batch_idx}/{len(batches)} for collection {collection_name}")

                documents = batch.get("documents", [])
                embeddings = batch.get("embeddings", [])
                metadatas = batch.get("metadatas", [])
                ids = batch.get("ids", [])

                imported_count = self.import_documents(
                    collection_name, documents, embeddings, metadatas, ids, batch_size
                )
                total_imported += imported_count

            logger.info(f"Imported {total_imported} total documents for collection {collection_name}")
            return True

        except Exception as e:
            logger.error(f"Error importing batched collection: {e}", exc_info=True)
            return False
