"""
Core search logic for Qdrant vector database queries.

This module contains the logic for performing searches across Qdrant collections.
It is called directly by the RAG server for all search operations.
"""

import logging
import warnings
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import Dict, List, Optional

from config import setup_logger
from rag.exceptions import is_not_found_error
from utils.config import Config

logger = logging.getLogger(__name__)
setup_logger()

# Shared thread pool for parallel collection searches
_search_executor = ThreadPoolExecutor(max_workers=Config.search_max_workers, thread_name_prefix="qdrant-search")


def _search_single_collection_by_vector(collection_name, query_vector, client, k, metadata_filter) -> List[dict]:
    """Search a single Qdrant collection using a pre-computed query vector."""
    try:
        query_filter = None
        if metadata_filter:
            from qdrant_client.models import Filter, FieldCondition, MatchValue

            conditions = []
            for key, value in metadata_filter.items():
                conditions.append(FieldCondition(key=f"metadata.{key}", match=MatchValue(value=value)))
            query_filter = Filter(must=conditions)  # type: ignore[arg-type]

        response = client.query_points(
            collection_name=collection_name,
            query=query_vector,
            limit=k,
            with_payload=True,
            query_filter=query_filter,
        )

        docs = []
        for point in response.points:
            normalized_score = max(0.0, min(1.0, (point.score + 1) / 2))
            payload = point.payload or {}
            page_content = payload.get("page_content", "")
            metadata = payload.get("metadata", {}) or {}
            # Surface the Qdrant point id as `metadata._id` when callers
            # didn't stamp one at ingest time. llm-server's memory parser
            # (`parseRAGMemoryResults`) keys off `_id` to map results back
            # to `llm_conversation_memory.id`; without this, every memory
            # search dropped 100% of results because the ingest path never
            # propagated the request's `metadata` dict into per-point payload.
            if isinstance(metadata, dict) and not metadata.get("_id"):
                metadata = {**metadata, "_id": str(point.id)}
            docs.append({"page_content": page_content, "metadata": metadata, "score": normalized_score})
        return docs
    except Exception as e:
        if is_not_found_error(e):
            logger.warning(f"Collection {collection_name} not found.")
        else:
            logger.error(f"Error searching collection {collection_name}: {e}", exc_info=True)
        return []


def search_collections(collection_names, query, account_id, min_results, metadata_filter: Optional[Dict] = None):
    """
    Performs a search on a list of Qdrant collections.
    Embeds the query once, then searches all collections with the pre-computed vector.
    Uses parallel execution when searching multiple collections.

    Args:
        collection_names: List of collection names to search
        query: Search query text
        account_id: Account ID for embeddings
        min_results: Minimum number of results to return per collection
        metadata_filter: Optional metadata filter for the search

    Returns:
        List of dicts with 'page_content', 'metadata', and 'score' keys
    """
    from rag.core.embeddings.generator import get_embeddings
    from rag.qdrant.client import get_qdrant_client

    warnings.filterwarnings("ignore", category=UserWarning, message="Relevance scores must be between 0 and 1.*")

    embeddings = get_embeddings(account_id)
    client = get_qdrant_client()
    k = max(5, min_results)

    # Embed query once, reuse vector across all collections
    query_vector = embeddings.embed_query(query)

    # Track search-time embedding token usage asynchronously to avoid
    # blocking the search response (~50-100ms for count_tokens API + DB insert)
    try:
        from rag.core.embeddings.tracker import persist_search_embedding_usage

        _search_executor.submit(persist_search_embedding_usage, account_id, embeddings, query)
    except Exception as e:
        logger.warning(f"Failed to submit search embedding tracking: {e}")

    # Single collection - no need for thread pool overhead
    if len(collection_names) == 1:
        return _search_single_collection_by_vector(collection_names[0], query_vector, client, k, metadata_filter)

    # Multiple collections - search in parallel
    all_docs = []
    futures = {
        _search_executor.submit(
            _search_single_collection_by_vector, name, query_vector, client, k, metadata_filter
        ): name
        for name in collection_names
    }

    for future in as_completed(futures):
        collection_name = futures[future]
        try:
            docs = future.result()
            all_docs.extend(docs)
        except Exception as e:
            logger.error(f"Unexpected error searching collection {collection_name}: {e}", exc_info=True)

    return all_docs
