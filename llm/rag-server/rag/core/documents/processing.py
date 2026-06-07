"""
Core document processing logic.

Handles batch processing, embedding generation, and document lifecycle management.
"""

import asyncio
import gc
import hashlib
import logging
import os
import time
from typing import List, Optional

from botocore.exceptions import ClientError
from rag.core.types import Document

from rag.core.documents.loaders.base import trim_text
from rag.core.embeddings.tracker import EmbeddingTracker
from rag.exceptions import is_not_found_error
from rag.qdrant.client import get_qdrant_client
from rag.vector_store import add_documents_to_vector_store, ensure_collection_exists
from sqlalchemy import text as sql_text

from utils.config import Config, DBConfig
from utils.shared import get_collection_name

logger = logging.getLogger(__name__)

# Module-level engine for connection pooling — avoid creating per-call.
_kb_engine = None


def _get_kb_engine():
    global _kb_engine
    if _kb_engine is None:
        from sqlalchemy import create_engine

        _kb_engine = create_engine(DBConfig.url, pool_pre_ping=True)
    return _kb_engine


def update_kb_load_result(
    knowledgebase_id: str,
    status: str,
    document_count: Optional[int] = None,
    error_message: Optional[str] = None,
) -> None:
    """Record a load's outcome on a single llm_knowledgebases row (by id).

    On success the caller passes the final document_count — status flips to
    'active', last_loaded_at is bumped, and error_message is cleared. On failure
    document_count is None, status → 'error', and error_message holds the reason
    so the UI can show *why*. Used for manual KBs.
    """
    try:
        engine = _get_kb_engine()
        with engine.connect() as connection:
            with connection.begin():
                if document_count is not None:
                    connection.execute(
                        sql_text(
                            "UPDATE llm_knowledgebases SET status = :status, "
                            "document_count = :doc_count, last_loaded_at = NOW(), "
                            "error_message = NULL, updated_at = NOW() WHERE id = :kb_id"
                        ),
                        {"status": status, "doc_count": document_count, "kb_id": knowledgebase_id},
                    )
                else:
                    connection.execute(
                        sql_text(
                            "UPDATE llm_knowledgebases SET status = :status, "
                            "error_message = :error_message, updated_at = NOW() WHERE id = :kb_id"
                        ),
                        {"status": status, "error_message": error_message, "kb_id": knowledgebase_id},
                    )
        logger.info(f"[KB] Load result for KB {knowledgebase_id}: status={status}, document_count={document_count}")
    except Exception as e:
        logger.error(f"[KB] Failed to update load result for KB {knowledgebase_id}: {e}")


def update_integration_kb_load_result(
    integration_id: str,
    status: str,
    document_count: Optional[int] = None,
    error_message: Optional[str] = None,
) -> None:
    """Record a load's outcome on every integration KB row for an integration.

    An integration can have one KB row per cloud account in its tenant, all
    backed by the same vector collection — so the update keys on integration_id
    rather than a single KB id. On failure error_message holds the reason; on
    success it is cleared.
    """
    try:
        engine = _get_kb_engine()
        with engine.connect() as connection:
            with connection.begin():
                if document_count is not None:
                    connection.execute(
                        sql_text(
                            "UPDATE llm_knowledgebases SET status = :status, "
                            "document_count = :doc_count, last_loaded_at = NOW(), "
                            "error_message = NULL, updated_at = NOW() "
                            "WHERE integration_id = :iid AND kb_type = 'integration'"
                        ),
                        {"status": status, "doc_count": document_count, "iid": integration_id},
                    )
                else:
                    connection.execute(
                        sql_text(
                            "UPDATE llm_knowledgebases SET status = :status, "
                            "error_message = :error_message, updated_at = NOW() "
                            "WHERE integration_id = :iid AND kb_type = 'integration'"
                        ),
                        {"status": status, "error_message": error_message, "iid": integration_id},
                    )
        logger.info(
            f"[KB] Load result for integration {integration_id}: status={status}, document_count={document_count}"
        )
    except Exception as e:
        logger.error(f"[KB] Failed to update load result for integration {integration_id}: {e}")


embeddings_generated_at = "embeddings_generated_at"


def _get_document_id(doc):
    """Extract or generate a document ID."""
    if hasattr(doc, "id"):
        return doc.id
    return hashlib.md5(doc.page_content.encode()).hexdigest()


# NOTE: _check_document_exists() function removed - it was causing major performance issues
# Old implementation checked existence of each document individually (N HTTP requests per batch)
# Qdrant's upsert operation handles duplicates automatically and atomically
# If you need the old function for reference, check git history


def _create_embeddings_batch(
    documents: List[Document],
    embeddings,
    collection_name,
    collection_metadata,
    tracker: Optional[EmbeddingTracker] = None,
    vector_store=None,
):
    """
    Create embeddings for a batch of documents.

    OPTIMIZED: Processes entire batch at once instead of one-at-a-time.
    This reduces API calls from N to 1 per batch.

    Args:
        documents: Batch of documents to process
        embeddings: Embedding function
        collection_name: Target collection name
        collection_metadata: Collection metadata
        tracker: Optional embedding tracker for metrics
        vector_store: Optional pre-created vector store for reuse across batches
    """
    if not documents:
        return

    logger.info(f"Generating embeddings for batch of {len(documents)} documents")

    # Process entire batch at once - uses bulk embedding API internally
    add_documents_to_vector_store(
        documents=documents,
        embedding_function=embeddings,
        collection_name=collection_name,
        collection_metadata=collection_metadata,
        vector_store=vector_store,
    )

    logger.info(f"Embeddings generated successfully for batch of {len(documents)} documents")

    # Track usage for each document if tracker is provided
    if tracker:
        usage_metadata = getattr(embeddings, "usage_metadata", None)
        for doc in documents:
            doc_id = getattr(doc, "id", _get_document_id(doc))
            tracker.record(
                doc_id=doc_id,
                text=doc.page_content,
                embedding_model=embeddings,
                usage_metadata=usage_metadata,
            )


async def generate_embeddings_batch(
    documents: List[Document],
    embeddings,
    collection_name,
    collection_metadata,
    tracker: EmbeddingTracker,
    verify_doc=False,
    vector_store=None,
):
    """
    Generates embeddings for a batch of documents with retry mechanism.

    OPTIMIZED: Processes entire batch at once instead of per-document.
    This is 100× faster than the old per-document approach.

    Args:
        documents: Batch of documents to process
        embeddings: Embedding function
        collection_name: Target collection name
        collection_metadata: Collection metadata
        tracker: Embedding tracker for metrics
        verify_doc: Whether to verify document existence before processing (deprecated - ignored)
        vector_store: Optional pre-created vector store for reuse across batches

    NOTE: verify_doc parameter is deprecated and ignored. Qdrant's upsert operation
    automatically handles duplicates, so existence checks are unnecessary and slow.
    """
    if not documents:
        return

    start_time = time.time()
    wait_time = int(os.environ.get("INITIAL_WAIT_TIME", 1))
    max_retry_duration = int(os.environ.get("MAX_RETRY_DURATION", 600))

    # NOTE: Removed existence check - Qdrant's upsert automatically handles duplicates
    # Old code: if verify_doc: for doc in documents: if not _check_document_exists()...
    # This was causing N individual HTTP requests for each document in the batch
    # Upsert is atomic and much faster - it updates existing docs or inserts new ones
    docs_to_process = documents

    # Main retry loop for entire batch
    while time.time() - start_time < max_retry_duration:
        try:
            # Process entire batch at once
            _create_embeddings_batch(
                documents=docs_to_process,
                embeddings=embeddings,
                collection_name=collection_name,
                collection_metadata=collection_metadata,
                tracker=tracker,
                vector_store=vector_store,
            )
            return
        except ClientError as ve:
            # Handle token limit errors
            if ve.response["Error"]["Code"] == "ValidationException":
                message = str(ve.response["Error"]["Message"])
                token_limit_error = (
                    "400 Bad Request: Too many input tokens" in message
                    or "Malformed input request: expected maxLength:" in message
                )

                if token_limit_error:
                    logger.info("Too many input tokens in batch. Retrying by reducing document sizes...")
                    # Trim all documents in batch
                    for doc in docs_to_process:
                        doc.page_content = trim_text(doc.page_content)
                        # Update document ID after content change
                        doc.id = _get_document_id(doc)
                    continue
            # Fall through to general retry logic
        except Exception as e:
            error_msg = str(e)

            # Detect non-retriable errors and fail fast
            # These errors cannot be fixed by retrying and will waste memory
            non_retriable_patterns = [
                "configured for dense vectors with",  # Dimension mismatch
                "dimensions. Selected embeddings are",  # Dimension mismatch
                "force_recreate",  # Collection config mismatch
            ]

            is_non_retriable = any(pattern in error_msg for pattern in non_retriable_patterns)

            if is_non_retriable:
                logger.error(
                    f"Non-retriable error detected for collection '{collection_name}': {error_msg}. "
                    f"This error cannot be fixed by retrying. Failing immediately to prevent memory accumulation."
                    f"\n\nTo fix this error:"
                    f"\n1. Delete and recreate the collection with correct dimensions"
                    f"\n2. Or change the embeddings model to match collection dimensions"
                    f"\n3. Collection: {collection_name}"
                )
                # Cleanup and return early (matches existing error handling pattern)
                del docs_to_process
                gc.collect()
                return  # Let caller handle failure

            # For retriable errors, log and continue retry loop
            logger.warning(f"Error while generating embeddings for batch: {error_msg}. Retrying...")

        # Wait before retry with exponential backoff
        await asyncio.sleep(wait_time)
        wait_time = min(wait_time * 2, 60)

    logger.error(f"Failed to generate embeddings for batch after {max_retry_duration} seconds.")
    # Cleanup on failure
    del docs_to_process
    gc.collect()


async def process_batch(
    batch_to_process, embeddings, collection_name, collection_meta, tracker, verify_doc, vector_store=None
):
    """
    Process a batch of documents.

    OPTIMIZED: Processes entire batch as a unit instead of creating async tasks per document.
    Old approach: N async tasks for N documents
    New approach: 1 batch operation for N documents (100× fewer API calls)
    """
    await generate_embeddings_batch(
        documents=batch_to_process,
        embeddings=embeddings,
        collection_name=collection_name,
        collection_metadata=collection_meta,
        tracker=tracker,
        verify_doc=verify_doc,
        vector_store=vector_store,
    )


def _build_collection_meta(account_id, module, tenant_id=None, source=None):
    """Assemble the collection-level metadata dict used by both the
    caller-supplied-vector-store path and ``_setup_collection``."""
    meta: dict = {
        "module": module,
        # UTC so timestamps are consistent across distributed workers.
        "embeddings_generated_at": time.strftime("%Y-%m-%d %H:%M:%S", time.gmtime()),
    }
    if account_id:
        meta["account"] = str(account_id)
    elif not tenant_id:
        meta["account"] = "global"
    if tenant_id:
        meta["tenant_id"] = str(tenant_id)
    if source:
        meta["source"] = source
    return meta


def _stamp_provenance(documents, source=None, tenant_id=None):
    """Stamp provenance tags onto each document's metadata so per-source and
    per-tenant filters work at query time. Respects caller overrides."""
    if not (source or tenant_id):
        return
    for doc in documents:
        if doc.metadata is None:
            doc.metadata = {}
        if source:
            doc.metadata.setdefault("source", source)
        if tenant_id:
            doc.metadata.setdefault("tenant_id", str(tenant_id))


def _setup_collection(account_id, module, collection_name, source=None, tenant_id=None):
    """Set up collection and determine verification needs."""
    # account / tenant are mutually-presented visibility axes. A tenant-scoped
    # collection must NOT default to ``account=global`` — that would leak it to
    # every tenant. _build_collection_meta handles the correct fallback.
    collection_meta = _build_collection_meta(account_id, module, tenant_id=tenant_id, source=source)
    if not collection_name:
        collection_name = get_collection_name(account_id, module)
    verify_doc = False

    try:
        from rag.core.metadata_cache import get_metadata_cache
        from rag.qdrant.client import list_collections_optimized

        # Ensure cache is populated
        list_collections_optimized()

        # Get from cache first
        cached_meta = get_metadata_cache().get(collection_name)
        if cached_meta:
            # Use cached metadata
            metadata = cached_meta["metadata"]
            if metadata:
                # Always make a static copy to avoid iteration errors.
                #
                # NOTE: source and tenant_id are set only at collection
                # creation time (see vector_store._create_collection). Qdrant
                # exposes no update_collection(metadata=...) API, so
                # pre-existing collections cannot be retro-tagged without a
                # drop-and-recreate. Search still reaches legacy collections
                # via the ``account`` metadata path in ``rag.py``.
                collection_meta = dict(metadata)
                if "embeddings_generated_at" in collection_meta:
                    logger.info(
                        f"Embeddings already generated for account id {account_id} and module {module}, "
                        "setting verification flag to True"
                    )
                    verify_doc = True
                else:
                    client = get_qdrant_client()
                    client.delete_collection(collection_name)
                    logger.info(f"Deleted collection: {collection_name}")
            else:
                logger.info("Collection metadata is empty. Deleting the existing collection before proceeding.")
                client = get_qdrant_client()
                client.delete_collection(collection_name)
                logger.info(f"Deleted collection: {collection_name}")
        else:
            # Collection doesn't exist in cache
            logger.info(f"Collection {collection_name} does not exist. Creating a new collection.")
            return collection_name, collection_meta, verify_doc

    except Exception as e:
        if is_not_found_error(e):
            logger.info(f"Collection {collection_name} does not exist. Creating a new collection.")
        else:
            logger.error(f"Error while fetching collection: {str(e)}")
            return None, None, None

    return collection_name, collection_meta, verify_doc


def _generate_document_ids(documents):
    """Generate hash IDs for documents."""
    documents_id = []
    logger.info("Generating hash ids for documents...")
    for doc in documents:
        if hasattr(doc, "id") and doc.id:
            doc_id = doc.id
        else:
            doc_id = hashlib.md5(doc.page_content.encode()).hexdigest()
            setattr(doc, "id", doc_id)
        documents_id.append(doc_id)
    logger.info("Hash ids generated for documents.")
    return documents_id


def process_documents(
    documents: List[Document],
    embeddings,
    account_id=None,
    module=None,
    collection_name=None,
    vector_store=None,
    knowledgebase_id=None,
    triggered_by="system",
    trigger_type="system_sync",
    source=None,
    tenant_id=None,
):
    """
    Process multiple documents asynchronously with batch embedding.

    Args:
        documents: List of documents to process
        embeddings: Embedding function
        account_id: Optional account ID
        module: Optional module name
        collection_name: Optional collection name
        vector_store: Optional pre-created vector store (skips collection setup when provided)
        knowledgebase_id: Optional KB ID for load tracking
        triggered_by: User ID or 'system' for automated loads
        trigger_type: 'user_create', 'user_update', 'user_retrigger', or 'system_sync'
        source: Optional provenance tag (e.g. ``confluence``, ``servicenow``,
            ``nudgebee_docs``, ``user_kb``). Stamped onto collection metadata
            and each document's payload so callers can filter by
            ``metadata_filter={"source": ...}`` at search time.
        tenant_id: Optional tenant ID for tenant-scoped collections. When set
            and ``account_id`` is omitted, the collection is visible to all
            cloud accounts belonging to this tenant via search-time filtering.

    Returns:
        Tuple of (collection_name, document_ids)
    """
    logger.info(
        f"Generating embeddings for account id {account_id}, tenant id {tenant_id}, "
        f"module {module}, collection {collection_name or get_collection_name(account_id, module)}, "
        f"source {source}"
    )

    if account_id is None and module is None and tenant_id is None:
        raise ValueError("account_id, tenant_id, or module should be provided")

    if vector_store is not None:
        # Caller already created the vector store — skip collection setup
        if not collection_name:
            collection_name = get_collection_name(account_id, module)
        collection_meta = _build_collection_meta(account_id, module, tenant_id=tenant_id, source=source)
        verify_doc = False
    else:
        # Setup collection and verify settings
        result = _setup_collection(account_id, module, collection_name, source=source, tenant_id=tenant_id)
        if result[0] is None:  # If collection setup failed
            return None, None
        collection_name, collection_meta, verify_doc = result

    _stamp_provenance(documents, source=source, tenant_id=tenant_id)

    # Initialize tracker with load metadata
    tracker = EmbeddingTracker(
        account_id,
        collection_name,
        knowledgebase_id=knowledgebase_id,
        triggered_by=triggered_by,
        trigger_type=trigger_type,
        module=module,
        expected_document_count=len(documents),
    )
    tracker.start_timer()

    # Generate document IDs
    documents_id = _generate_document_ids(documents)

    # Process documents
    logger.info(f"Processing documents for account id {account_id}, module {module}, collection {collection_name}")

    logger.info("Starting document processing...")
    try:
        process_start_time = time.time()
        total_documents = len(documents)
        logger.info(f"Processing a total of {total_documents} documents using batch size {Config.embedding_batch_size}")

        if documents:
            asyncio.run(
                process_all_batches(
                    documents,
                    Config.embedding_batch_size,
                    embeddings,
                    collection_name,
                    collection_meta,
                    tracker,
                    verify_doc,
                    vector_store=vector_store,
                )
            )

        process_duration = time.time() - process_start_time
        logger.info(f"Document processing completed successfully in {process_duration:.2f}s")
    except RuntimeError as e:
        logger.error(f"Error running event loop: {e}")
        tracker.persist(status="failure", error_message=str(e))
        if knowledgebase_id:
            update_kb_load_result(knowledgebase_id, "error", error_message=str(e))
        return collection_name, []
    except Exception as e:
        logger.error(f"Unexpected error during document processing: {str(e)}")
        tracker.persist(status="failure", error_message=str(e))
        if knowledgebase_id:
            update_kb_load_result(knowledgebase_id, "error", error_message=str(e))
        return collection_name, []

    # Persist all tracked records
    tracker.persist()

    # rag-server owns the terminal status flip for KBs. Manual KBs pass their
    # knowledgebase_id here; integration scrapes pass none and update every
    # integration KB row by integration_id at scrape completion instead.
    if knowledgebase_id:
        update_kb_load_result(knowledgebase_id, "active", tracker.expected_document_count or total_documents)

    logger.info(
        f"Embeddings generated for account id {account_id}, module {module}, "
        f"collection {collection_name}. Cleaning up..."
    )
    del documents
    logger.info("Garbage collecting...")
    gc.collect()
    return collection_name, documents_id


async def process_all_batches(
    documents_for_processing,
    batch_size,
    embeddings,
    collection_name,
    collection_meta,
    tracker,
    verify_doc,
    vector_store=None,
):
    """Process all batches of documents with controlled concurrency.

    Uses an asyncio.Queue with a fixed worker pool to avoid creating all tasks
    upfront. Only `max_concurrency` batches are in-flight at any time, keeping
    memory usage proportional to concurrency, not total batch count.
    """
    total_docs = len(documents_for_processing)
    total_batches = (total_docs + batch_size - 1) // batch_size
    processed_docs = 0
    start_time = time.time()
    counter_lock = asyncio.Lock()

    gc_interval = Config.gc_interval

    logger.info(
        f"Starting to process {total_docs} documents in {total_batches} batches (batch size: {batch_size}, "
        f"concurrency: {Config.max_concurrency})..."
    )

    # Reuse caller-provided vector store, or create one for this batch run
    if vector_store is None:
        vector_store = ensure_collection_exists(
            embedding_function=embeddings,
            collection_name=collection_name,
            collection_metadata=collection_meta,
        )

    queue: asyncio.Queue = asyncio.Queue()
    for batch_number, start_index in enumerate(range(0, total_docs, batch_size)):
        queue.put_nowait((batch_number, start_index))

    async def worker():
        nonlocal processed_docs
        local_count = 0
        while True:
            try:
                batch_num, start_idx = queue.get_nowait()
            except asyncio.QueueEmpty:
                break

            batch_start_time = time.time()
            end_idx = min(start_idx + batch_size, total_docs)
            batch = documents_for_processing[start_idx:end_idx]
            batch_size_actual = len(batch)

            logger.info(
                f"Processing batch {batch_num + 1}/{total_batches}: documents {start_idx + 1}-{end_idx} "
                f"({batch_size_actual} documents)"
            )
            await process_batch(
                batch, embeddings, collection_name, collection_meta, tracker, verify_doc, vector_store=vector_store
            )
            batch_duration = time.time() - batch_start_time

            # Drop reference to processed batch slice so it can be collected
            del batch

            async with counter_lock:
                processed_docs += batch_size_actual
                current_processed = processed_docs

            logger.info(
                f"Completed batch {batch_num + 1}/{total_batches}: processed {current_processed}/{total_docs} "
                f"documents ({(current_processed / total_docs) * 100:.1f}%) in {batch_duration:.2f}s"
            )

            # Periodic garbage collection to reclaim memory from completed batches
            local_count += 1
            if local_count % gc_interval == 0:
                collected = gc.collect()
                if collected:
                    logger.info(f"GC collected {collected} objects at batch {batch_num + 1}")

            queue.task_done()

    workers = [asyncio.create_task(worker()) for _ in range(Config.max_concurrency)]
    await asyncio.gather(*workers)

    total_duration = time.time() - start_time
    logger.info(
        f"All batches processed: {processed_docs}/{total_docs} documents in {total_duration:.2f}s "
        f"({total_duration / total_docs:.4f}s per document)"
    )


def _delete_document(client, doc_id, collection_name):
    """Helper function to delete a single document from a collection using direct Qdrant client."""
    try:
        # Retrieve document to log it
        points = client.retrieve(
            collection_name=collection_name,
            ids=[doc_id],
            with_payload=True,
            with_vectors=False,
        )
        if points:
            payload = points[0].payload or {}
            data = payload.get("page_content", "")
            logger.info(f"Deleting missing doc id: {doc_id}, data: {data}, collection: {collection_name}")

        # Delete the point
        client.delete(collection_name=collection_name, points_selector=[doc_id])
        logger.info(f"Deleted missing document: {doc_id} from collection: {collection_name}")
        return True
    except Exception as e:
        logger.error(f"Error while deleting missing doc: {doc_id} from collection: {collection_name}: {e}")
        return False


def _get_missing_doc_ids(client, collection_name, documents_id):
    """Helper function to identify documents that need to be deleted using direct Qdrant client."""
    try:
        # Scroll through all points to get existing IDs
        all_ids = []
        offset = None
        while True:
            points, next_offset = client.scroll(
                collection_name=collection_name,
                limit=100,
                offset=offset,
                with_payload=False,
                with_vectors=False,
            )
            if not points:
                break
            all_ids.extend([str(point.id) for point in points])
            if next_offset is None:
                break
            offset = next_offset

        logger.info(f"Length of documents processed: {len(documents_id)}")
        existing_ids = set(all_ids)
        logger.info(f"Length of existing documents: {len(existing_ids)}")
        # Normalize IDs by removing hyphens for comparison.
        # Qdrant stores UUIDs with hyphens (e.g. "3c73415d-dce9-..."),
        # but _generate_document_ids returns plain hex (e.g. "3c73415ddce9...").
        normalized_doc_ids = {doc_id.replace("-", "") for doc_id in documents_id}
        missing_ids = {eid for eid in existing_ids if eid.replace("-", "") not in normalized_doc_ids}
        logger.info(f"Missing ids: {len(missing_ids)}")
        return missing_ids
    except Exception as e:
        logger.error(f"Error getting missing doc IDs: {e}")
        return set()


def handle_updated_documents(collection_name, documents_id) -> bool:
    """Delete documents from collection that are not in documents_id list using direct Qdrant client."""
    is_documents_updated = False

    try:
        client = get_qdrant_client()

        # Check if collection exists
        try:
            client.get_collection(collection_name)
        except Exception:
            return False

        missing_ids = _get_missing_doc_ids(client, collection_name, documents_id)
        for missing_id in missing_ids:
            if _delete_document(client, missing_id, collection_name):
                is_documents_updated = True

        gc.collect()

    except Exception as e:
        if is_not_found_error(e):
            logger.info(f"Collection {collection_name} does not exist. Ignoring missing document deletion.")
        else:
            logger.error(f"Error while fetching collection: {str(e)}")
            raise

    return is_documents_updated
