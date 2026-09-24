import logging
from typing import List, Any
from urllib.parse import unquote_plus, urlsplit

import httpx
from qdrant_client.http.exceptions import ResponseHandlingException

from rag.qdrant.client import get_qdrant_client
from rag.qdrant.client import list_collections_optimized

logger = logging.getLogger(__name__)


def has_collections() -> bool:
    """
    Fast check if any collections exist in Qdrant.
    Uses optimized cached collection list.
    """
    try:
        collections = get_qdrant_client().get_collections()
        if len(collections.collections) > 0:
            logger.info(f"Found {len(collections.collections)} collections in Qdrant")
            return True
        return False
    except (
        ConnectionError,
        ResponseHandlingException,
        httpx.RequestError,
        httpx.TimeoutException,
        httpx.ConnectError,
    ) as e:
        logger.warning(f"Cannot connect to Qdrant during startup - skipping collection loading: {e}")
        logger.info("Collections will need to be loaded manually when Qdrant becomes available")
        raise
    except Exception as e:
        logger.error(f"Error checking collections existence: {e}")
        return False


def _should_include_collection(collection: Any, module: str | None) -> bool:
    """
    Check if collection should be included based on module filter.

    Args:
        collection: CollectionInfo object from list_collections_optimized()
        module: Module name to filter by (None = include all)

    Returns:
        True if collection should be included, False otherwise
    """
    if not module:
        return True
    return collection.metadata and "module" in collection.metadata and module == collection.metadata["module"]


def _build_collection_info(collection: Any) -> dict:
    """
    Build collection info dictionary from CollectionInfo object.

    Args:
        collection: CollectionInfo object from list_collections_optimized()

    Returns:
        Collection info dictionary
    """
    metadata = collection.metadata
    logger.debug(f"Collection {collection.name} metadata: {metadata}, type: {type(metadata)}")

    return {
        "name": collection.name,
        "count": collection.points_count,
        "id": collection.name,  # Use name as ID for Qdrant
        "metadata": metadata,
    }


def _log_collection_summary(response: list, module: str | None) -> None:
    """
    Log summary of collections found.

    Args:
        response: List of collection info dictionaries
        module: Module filter (if any)
    """
    if module:
        if not response:
            logger.warning(f"No collections found with module name {module}")
        else:
            logger.info(f"Found {len(response)} collections with module name {module}")
    else:
        logger.info(f"Listed {len(response)} collections")


def list_collections(module: str | None = None) -> List[dict]:
    """
    List collections with optimized caching.

    Uses list_collections_optimized() which returns CollectionInfo objects
    with metadata and counts already fetched and cached (30-min TTL).

    Args:
        module: Filter collections by module name

    Returns:
        List of collection info dictionaries with count and metadata
    """
    collections = list_collections_optimized()

    response = []
    total_documents = 0

    for collection in collections:
        # Skip collections that don't match module filter
        if not _should_include_collection(collection, module):
            continue

        # Build collection info dictionary
        collection_info = _build_collection_info(collection)
        response.append(collection_info)

        # Track totals
        total_documents += collection.points_count

    # Log summary
    _log_collection_summary(response, module)

    logger.info(f"Listed {len(response)} collection(s) with {total_documents:,} total documents")

    return response


def _fetch_all_documents(client, collection_name: str, limit: int) -> List[str]:
    """Fetch all documents from collection using scroll."""
    documents: List[str] = []
    offset = None

    while len(documents) < limit:
        points, next_offset = client.scroll(
            collection_name=collection_name,
            limit=min(limit - len(documents), 100),
            offset=offset,
            with_payload=True,
            with_vectors=False,
        )

        if not points:
            break

        for point in points:
            payload = point.payload or {}
            doc_content = payload.get("page_content", "")
            if doc_content:
                documents.append(doc_content)

        if next_offset is None or len(documents) >= limit:
            break
        offset = next_offset

    return documents


def _semantic_search(client, collection_name: str, query: str, account_id: str, limit: int) -> List[str]:
    """Perform semantic search on collection."""
    from rag.core.embeddings.generator import get_embeddings
    from rag.vector_store import get_vector_store

    embeddings = get_embeddings(account_id)
    store = get_vector_store(collection_name=collection_name, embedding_function=embeddings, client=client)

    k = max(5, limit)
    result_with_scores = store.similarity_search_with_relevance_scores(query, k)

    if result_with_scores:
        return [doc.page_content for doc, score in result_with_scores[:limit]]
    return []


def query_collection(collection_name: str, query: str, limit=10) -> List[str] | list[list[str]] | None:
    """
    Query a collection using semantic search or return all documents.

    Args:
        collection_name: Name of the collection to query
        query: Search query text (empty string returns all documents)
        limit: Maximum number of results to return

    Returns:
        List of document strings, or None if collection not found
    """
    logger.info(f"Querying collection {collection_name} with query: {query}")

    try:
        from rag.core.metadata_cache import get_metadata_cache
        from rag.qdrant.client import list_collections_optimized

        client = get_qdrant_client()

        # Ensure cache is populated
        list_collections_optimized()

        # Get metadata from cache
        cached_meta = get_metadata_cache().get(collection_name)
        metadata: dict[str, Any] = {}
        if cached_meta:
            metadata = cached_meta["metadata"] or {}

        if query == "":
            # Empty query: Return all documents
            logger.info(f"Query is empty, returning documents from collection {collection_name}")
            return _fetch_all_documents(client, collection_name, limit)
        else:
            # Non-empty query: Perform semantic search
            logger.info(f"Performing semantic search on collection {collection_name}")
            account_id = metadata.get("account", "global")
            return _semantic_search(client, collection_name, query, account_id, limit)

    except Exception as e:
        from rag.exceptions import is_not_found_error

        if is_not_found_error(e):
            logger.warning(f"Collection {collection_name} does not exist")
            return None
        logger.error(f"Error querying collection {collection_name}: {e}")
        raise


def delete_collection(collection_name: str):
    logger.info(f"Deleting collection {collection_name}")
    try:
        client = get_qdrant_client()
        client.delete_collection(collection_name)
        logger.info(f"Deleted collection {collection_name}")

        # Invalidate cache after deletion
        from rag.core.cache import get_collection_list_cache
        from rag.core.metadata_cache import get_metadata_cache

        get_collection_list_cache().invalidate()
        get_metadata_cache().invalidate()
    except ValueError:
        logger.warning(f"Failed to delete collection {collection_name}, as it does not exist.")
    except Exception as e:
        logger.error(f"Failed to delete collection {collection_name}: {e}")


def delete_module_collections(module_name: str):
    logger.info(f"Deleting collections with module name {module_name}")
    try:
        client = get_qdrant_client()
        collections = list_collections_optimized()
        for collection in collections:
            if collection.metadata:
                if "module" in collection.metadata and module_name == collection.metadata["module"]:
                    client.delete_collection(collection.name)
                    logger.info(f"Deleted collection name: {collection.name}")
        logger.info(f"Deleted all collections with module name: {module_name}")

        # Invalidate cache after deletion
        from rag.core.cache import get_collection_list_cache
        from rag.core.metadata_cache import get_metadata_cache

        get_collection_list_cache().invalidate()
        get_metadata_cache().invalidate()
    except ValueError:
        logger.warning(f"Failed to delete collection {module_name}, as it does not exist.")
    except Exception as e:
        logger.error(f"Failed to delete collection {module_name}: {e}")


def delete_all_collections():
    logger.info("Deleting all collections")
    try:
        client = get_qdrant_client()
        collections = list_collections_optimized()
        for collection in collections:
            logger.info(f"Deleting collection name: {collection.name}")
            client.delete_collection(collection.name)

        # Invalidate cache after deletion
        from rag.core.cache import get_collection_list_cache
        from rag.core.metadata_cache import get_metadata_cache

        get_collection_list_cache().invalidate()
        get_metadata_cache().invalidate()
    except Exception as e:
        logger.error(f"Failed to delete collections: {e}")


def delete_document_by_id(collection_name: str, doc_id: str) -> bool:
    """
    Delete a single document from a collection by its ID.

    Args:
        collection_name: Name of the collection
        doc_id: ID of the document to delete

    Returns:
        True if deletion was successful (or document didn't exist), False on error
    """
    logger.info(f"Deleting document {doc_id} from collection {collection_name}")
    try:
        from rag.core.metadata_cache import get_metadata_cache
        from rag.qdrant.client import list_collections_optimized

        client = get_qdrant_client()

        # Check if collection exists in cache first
        list_collections_optimized()
        if not get_metadata_cache().exists(collection_name):
            logger.warning(f"Collection {collection_name} does not exist")
            return True

        client.delete(collection_name=collection_name, points_selector=[doc_id])
        logger.info(f"Deleted document {doc_id} from collection {collection_name}")
        return True
    except Exception as e:
        logger.error(f"Failed to delete document {doc_id} from collection {collection_name}: {e}")
        return False


def get_collection(collection_name: str, limit: int | None = None) -> dict | None:
    """
    Get collection details with documents.

    Args:
        collection_name: Name of the collection
        limit: Optional limit on number of documents to return

    Returns:
        Dictionary with collection info and documents, or None if not found
    """
    logger.info(f"Getting collection {collection_name}")

    try:
        from rag.core.metadata_cache import get_metadata_cache
        from rag.qdrant.client import list_collections_optimized

        client = get_qdrant_client()

        # Ensure cache is populated
        list_collections_optimized()

        # Get collection info from cache
        cached_meta = get_metadata_cache().get(collection_name)
        if not cached_meta:
            logger.warning(f"Collection {collection_name} not found")
            return None

        # Extract metadata
        metadata = cached_meta["metadata"] or {}

        # Get points count
        points_count = cached_meta["points_count"]

        # Fetch documents
        documents_data = []
        offset = None
        docs_fetched = 0
        fetch_limit = limit if limit else points_count

        while docs_fetched < fetch_limit:
            points, next_offset = client.scroll(
                collection_name=collection_name,
                limit=min(fetch_limit - docs_fetched, 100),
                offset=offset,
                with_payload=True,
                with_vectors=False,
            )

            if not points:
                break

            for point in points:
                payload = point.payload or {}
                doc_content = payload.get("page_content", "")
                # Remove page_content from metadata
                doc_metadata = {k: v for k, v in payload.items() if k != "page_content"}

                documents_data.append(
                    {
                        "id": str(point.id),
                        "document": doc_content,
                        "metadata": doc_metadata,
                    }
                )
                docs_fetched += 1

            if next_offset is None or docs_fetched >= fetch_limit:
                break
            offset = next_offset

        logger.info(f"Collection {collection_name} loaded {len(documents_data)} documents")

        return {
            "name": collection_name,
            "count": points_count,
            "id": collection_name,
            "metadata": metadata,
            "data": documents_data,
        }

    except Exception as e:
        from rag.exceptions import is_not_found_error

        if is_not_found_error(e):
            logger.warning(f"Collection {collection_name} does not exist")
            return None
        logger.error(f"Error getting collection {collection_name}: {e}")
        raise


def _document_title(metadata: dict, content: str) -> str | None:
    """Title for a stored document: metadata ``title`` (Confluence), else the
    ``Title: ...`` first line the ServiceNow scraper prepends to the text, else
    the page slug of a Confluence URL (pages scraped before ``title`` was stored)."""
    title = metadata.get("title")
    if isinstance(title, str) and title.strip():
        return title.strip()
    first_line = content.lstrip().split("\n", 1)[0]
    if first_line.startswith("Title:"):
        return first_line[len("Title:") :].strip() or None
    url = _document_url(metadata)
    if url:
        # .../pages/<page_id>/<Page+Title>
        segments = urlsplit(url).path.rstrip("/").split("/")
        if len(segments) >= 3 and segments[-3] == "pages" and not segments[-1].isdigit():
            return unquote_plus(segments[-1]).strip() or None
    return None


def _document_url(metadata: dict) -> str | None:
    url = metadata.get("url")
    return url if isinstance(url, str) and url.startswith(("http://", "https://")) else None


def _point_metadata(payload: dict) -> dict:
    # Points are upserted as {"page_content": ..., "metadata": {...}}.
    metadata = payload.get("metadata")
    return metadata if isinstance(metadata, dict) else {}


def _parse_point_id(point_id: str) -> int | str:
    return int(point_id) if point_id.isdigit() else point_id


def _document_content(payload: dict) -> str:
    """Full text of the source document. Chunked documents carry the whole
    original text on every chunk as ``metadata.parent_content``."""
    parent_content = _point_metadata(payload).get("parent_content")
    if isinstance(parent_content, str) and parent_content:
        return parent_content
    content = payload.get("page_content")
    return content if isinstance(content, str) else ""


# The metadata key each scrape stamps its document id on. A new scrape adds its key
# here. Public because search results must carry the same keys
# (controllers/knowledge_document.py): llm-server keys a document's Fact/SOP mark on them.
SOURCE_ID_KEYS = ("source_id", "page_id", "sys_id")


def _source_identifier(metadata: dict) -> str | None:
    """The id the scrape stamped on this document, or None when it stamped none."""
    for key in SOURCE_ID_KEYS:
        value = metadata.get(key)
        # 0 is a legitimate id; "" and None are not. Truthiness alone would drop the
        # first, and `is not None` alone would keep the second as a blank id.
        if value is None or value == "":
            continue
        return str(value)
    return None


def list_collection_documents(collection_name: str, limit: int = 50, offset: str | None = None) -> dict:
    """
    List one page of a collection's documents as title/link entries, without content.

    Integration scrapers store one point per source page/article. Chunked
    documents store one point per chunk sharing ``metadata.parent_id``; those
    collapse to a single entry so the list matches the KB's document count.
    Chunks of one document that straddle a page boundary can repeat on the next
    page. ``next_offset`` is Qdrant's scroll cursor; None means last page.
    """
    points, next_offset = get_qdrant_client().scroll(
        collection_name=collection_name,
        limit=limit,
        offset=_parse_point_id(offset) if offset else None,
        with_payload=True,
        with_vectors=False,
    )
    items = []
    seen_parents = set()
    for point in points:
        payload = point.payload or {}
        metadata = _point_metadata(payload)
        parent_id = metadata.get("parent_id")
        if parent_id:
            if parent_id in seen_parents:
                continue
            seen_parents.add(parent_id)
        items.append(
            {
                "id": str(point.id),
                "title": _document_title(metadata, _document_content(payload)),
                "url": _document_url(metadata),
                # The scraper's own id for the page: what a Fact/SOP mark is keyed on,
                # because unlike the point id it survives a re-sync.
                "source_id": _source_identifier(metadata),
            }
        )
    return {"items": items, "next_offset": str(next_offset) if next_offset is not None else None}


def get_collection_document(collection_name: str, point_id: str) -> dict | None:
    """Return one document with its full content, or None when the point is gone
    (point ids are content hashes, so a re-sync can replace them)."""
    points = get_qdrant_client().retrieve(
        collection_name=collection_name,
        ids=[_parse_point_id(point_id)],
        with_payload=True,
        with_vectors=False,
    )
    if not points:
        return None
    payload = points[0].payload or {}
    metadata = _point_metadata(payload)
    content = _document_content(payload)
    return {
        "id": str(points[0].id),
        "title": _document_title(metadata, content),
        "url": _document_url(metadata),
        "source_id": _source_identifier(metadata),
        "content": content,
    }
