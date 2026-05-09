"""
Vector Store module for Qdrant.

Provides interface for creating and using vector stores with Qdrant.
Uses qdrant-client directly instead of langchain wrappers.
"""

import logging
import uuid
from typing import Any, Dict, List, Optional

from qdrant_client.http.exceptions import UnexpectedResponse
from qdrant_client.models import Distance, PointStruct, VectorParams

from rag.core.types import Document

logger = logging.getLogger(__name__)

# HTTP status codes indicating the collection already exists
_COLLECTION_EXISTS_STATUS_CODES = {409, 400}


class DirectVectorStore:
    """Thin wrapper around qdrant_client for add_documents compatibility."""

    def __init__(self, client: Any, collection_name: str, embedding_function: Any):
        self.client = client
        self.collection_name = collection_name
        self.embedding_function = embedding_function

    def add_documents(self, documents: List[Document]) -> None:
        """Embed documents and upsert into Qdrant."""
        if not documents:
            return

        texts = [doc.page_content for doc in documents]
        vectors = self.embedding_function.embed_documents(texts)

        points = []
        for doc, vector in zip(documents, vectors):
            point_id = doc.id or str(uuid.uuid4())
            payload = {
                "page_content": doc.page_content,
                "metadata": doc.metadata,
            }
            points.append(PointStruct(id=point_id, vector=vector, payload=payload))

        self.client.upsert(collection_name=self.collection_name, points=points)


def _create_collection(client, collection_name: str, vector_size: int, metadata: Dict[str, Any]) -> bool:
    """Create a Qdrant collection. Returns True if created, False if it already exists.

    Raises:
        UnexpectedResponse: If the error is not a "collection already exists" conflict.
    """
    try:
        client.create_collection(
            collection_name=collection_name,
            vectors_config=VectorParams(size=vector_size, distance=Distance.COSINE),
            metadata=metadata,
        )
        logger.info(f"Created collection {collection_name} with vector size {vector_size}")
        return True
    except UnexpectedResponse as e:
        if e.status_code in _COLLECTION_EXISTS_STATUS_CODES:
            logger.debug(f"Collection {collection_name} already exists (HTTP {e.status_code})")
            return False
        raise


def ensure_collection_exists(
    embedding_function: Any,
    collection_name: str,
    collection_metadata: Optional[Dict[str, Any]] = None,
) -> DirectVectorStore:
    """
    Ensure a Qdrant collection exists and return a reusable DirectVectorStore.

    Call this once before batch processing to create the collection if needed
    and get a vector store instance that can be reused across all batches.

    Args:
        embedding_function: Embedding function
        collection_name: Name of the collection
        collection_metadata: Optional metadata for the collection

    Returns:
        DirectVectorStore instance for reuse across batches
    """
    from rag.qdrant.client import get_qdrant_client
    from rag.core.cache import get_collection_list_cache
    from rag.qdrant.client import list_collections_optimized

    qdrant_client = get_qdrant_client()

    test_embedding = embedding_function.embed_query("probe for vector size")
    vector_size = len(test_embedding)

    list_collections_optimized()

    cache = get_collection_list_cache()
    exists_in_cache = cache.exists(collection_name)
    collection_created = False

    if exists_in_cache:
        logger.debug(f"Collection {collection_name} exists (cached)")
    elif exists_in_cache is False or exists_in_cache is None:
        # Collection doesn't exist or cache is expired/unknown — try to create
        collection_created = _create_collection(qdrant_client, collection_name, vector_size, collection_metadata or {})

    if collection_created:
        from rag.core.metadata_cache import get_metadata_cache
        from rag.qdrant.client import CollectionInfo

        # Add new collection to both caches instead of invalidating (avoids expensive N+1 re-fetch)
        metadata = collection_metadata or {}
        cache.add(CollectionInfo(name=collection_name, metadata=metadata, points_count=0))
        get_metadata_cache().set(collection_name, metadata, points_count=0)

    return DirectVectorStore(
        client=qdrant_client,
        collection_name=collection_name,
        embedding_function=embedding_function,
    )


def add_documents_to_vector_store(
    documents: List[Document],
    embedding_function: Any,
    collection_name: str,
    collection_metadata: Optional[Dict[str, Any]] = None,
    vector_store: Optional[DirectVectorStore] = None,
):
    """
    Add documents to a Qdrant collection.

    When a vector_store is provided (recommended for batch processing), documents
    are added directly without redundant collection checks or VectorStore creation.

    Args:
        documents: List of documents
        embedding_function: Embedding function
        collection_name: Name of the collection
        collection_metadata: Optional metadata for the collection
        vector_store: Optional pre-created vector store (from ensure_collection_exists)

    Returns:
        None (documents are added to Qdrant)
    """
    if not vector_store:
        vector_store = ensure_collection_exists(
            embedding_function=embedding_function,
            collection_name=collection_name,
            collection_metadata=collection_metadata,
        )

    vector_store.add_documents(documents=documents)
    logger.info(f"Added {len(documents)} documents to collection {collection_name}")


def get_vector_store(collection_name: str, embedding_function: Any, client: Any = None):
    """
    Get an existing vector store for querying.

    Args:
        collection_name: Name of the collection
        embedding_function: Embedding function
        client: Optional Qdrant client (uses shared client if not provided)

    Returns:
        DirectVectorStore instance
    """
    from rag.qdrant.client import get_qdrant_client

    qdrant_client = client if client else get_qdrant_client()

    return DirectVectorStore(
        client=qdrant_client,
        collection_name=collection_name,
        embedding_function=embedding_function,
    )
