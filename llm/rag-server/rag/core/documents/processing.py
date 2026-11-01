"""
Core document processing logic.

Handles batch processing, embedding generation, and document lifecycle management.
"""

import asyncio
import copy
import gc
import hashlib
import json
import logging
import os
import time
import uuid
from concurrent.futures import ThreadPoolExecutor
from typing import List, Optional

from botocore.exceptions import ClientError
from rag.core.types import Document

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

# Ids per existence-probe request. Qdrant carries the id list in the request body,
# so a whole embedding batch is chunked rather than sent as one oversized call.
_EXISTENCE_PROBE_CHUNK = 256


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
    warning: Optional[str] = None,
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
                    # The warning rides in the same statement as the status flip.
                    # Written separately afterwards it would be erased by this
                    # very UPDATE, and a gap nobody is shown is a silently
                    # partial index.
                    connection.execute(
                        sql_text(
                            "UPDATE llm_knowledgebases SET status = :status, "
                            "document_count = :doc_count, last_loaded_at = NOW(), "
                            "error_message = :warning, updated_at = NOW() WHERE id = :kb_id"
                        ),
                        {
                            "status": status,
                            "doc_count": document_count,
                            "warning": warning or None,
                            "kb_id": knowledgebase_id,
                        },
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
    success it holds any shortfall worth reporting — a load that indexed the text
    but dropped the pictures is active, not failed, and must still say so — and is
    cleared when there is nothing to report.
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
                            "error_message = :error_message, updated_at = NOW() "
                            "WHERE integration_id = :iid AND kb_type = 'integration'"
                        ),
                        {
                            "status": status,
                            "doc_count": document_count,
                            "iid": integration_id,
                            "error_message": error_message or None,
                        },
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


# NOTE: the original _check_document_exists() did one HTTP round-trip per document
# (N per batch) and was removed for that reason. Upsert does dedupe *storage* — point
# ids are content hashes, so an unchanged page overwrites itself — but the vector is
# computed before Qdrant is ever contacted, so unchanged pages were still being
# re-embedded on every sync. This is the batched form: one retrieve per batch instead
# of N, ids only, no payload or vectors.
def _normalize_point_id(point_id):
    """Hyphen-stripped form of a point id, for comparison only.

    Qdrant accepts a bare 32-char hex id on write but stores and returns it in
    canonical hyphenated UUID form, so `_generate_document_ids`' plain hex never
    equals a returned `point.id` by string compare. Same normalisation as
    `_get_missing_doc_ids`.
    """
    return str(point_id).replace("-", "")


def _existing_point_ids(collection_name, ids):
    """Return the normalised subset of `ids` already present in the collection.

    Chunked because Qdrant takes the id list in the request body and a whole
    embedding batch can be large. Ids only — no payload, no vectors — so this
    stays a cheap existence probe rather than a read.
    """
    existing = set()
    client = get_qdrant_client()
    for start in range(0, len(ids), _EXISTENCE_PROBE_CHUNK):
        chunk = ids[start : start + _EXISTENCE_PROBE_CHUNK]
        found = client.retrieve(
            collection_name=collection_name,
            ids=chunk,
            with_payload=False,
            with_vectors=False,
        )
        existing.update(_normalize_point_id(point.id) for point in found)
    return existing


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
                # Chunks of one document share a parent_id; an unchunked document
                # is its own key.
                document_key=(doc.metadata or {}).get("parent_id") or doc_id,
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
        verify_doc: Skip documents already present in the collection (set by _setup_collection)
        vector_store: Optional pre-created vector store for reuse across batches

    NOTE: verify_doc gates the batched existence probe. Upsert dedupes storage, but
    the embedding is computed before Qdrant is contacted, so skipping unchanged
    documents is what avoids the redundant embedding work.
    """
    if not documents:
        return

    start_time = time.time()
    wait_time = int(os.environ.get("INITIAL_WAIT_TIME", 1))
    max_retry_duration = int(os.environ.get("MAX_RETRY_DURATION", 600))

    # Skip documents already embedded in this collection. `verify_doc` is only set
    # when the collection exists AND carries embeddings_generated_at (see
    # _setup_collection), i.e. exactly the re-sync case — on a first build there is
    # nothing to find, so the probe is skipped entirely. Fails open: any error here
    # embeds the full batch rather than blocking ingestion.
    docs_to_process = documents
    if verify_doc and documents:
        try:
            batch_ids = [_get_document_id(d) for d in documents]
            existing = _existing_point_ids(collection_name, batch_ids)
            if existing:
                docs_to_process = [
                    d for d, doc_id in zip(documents, batch_ids) if _normalize_point_id(doc_id) not in existing
                ]
                logger.info(
                    f"Skipping {len(documents) - len(docs_to_process)} already-embedded "
                    f"documents of {len(documents)} in batch"
                )
            if not docs_to_process:
                return
        except Exception as e:
            logger.warning(f"Existence probe failed, embedding full batch: {e}")
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
                    # Imported locally: `loaders` imports this module back, so a
                    # module-scope import here is a cycle that decides which test
                    # files are collectable. The dependency only matters on the
                    # oversize-batch retry path.
                    from rag.core.documents.loaders.base import trim_text

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


# The reranker hard-slices each document to Config.reranker_max_doc_chars
# characters BEFORE tokenizing it (local_reranker.py:rerank, applied ahead of
# the separate Config.reranker_max_length combined-token-budget truncation),
# so any chunk longer than that has its tail permanently invisible to
# scoring regardless of tokenizer behavior. The worst-case stored chunk is
# _CHUNK_CHAR_LIMIT + _CHUNK_OVERLAP_CHARS (see _split_on_paragraphs's final
# flush), so the overlap must come out of the same budget, not sit on top of
# it. An earlier fixed 700-char limit was sized against an assumed reranker
# config, not the one main was running (500 chars / 128 tokens since
# 604d28a76c), and live-testing showed content past char 500 of such a chunk
# is never scored. Deriving the limit from the live config instead of a
# disconnected guess keeps it from drifting out of sync again.
_CHUNK_OVERLAP_CHARS = 100
_CHUNK_CHAR_LIMIT = max(200, Config.reranker_max_doc_chars - _CHUNK_OVERLAP_CHARS)


def _chunk_long_documents(documents):
    """Split any document longer than _CHUNK_CHAR_LIMIT into smaller chunks
    that fit the reranker's effective budget, on paragraph boundaries where
    possible. Metadata already set on the document (provenance, any classifier
    output a caller has already run) is copied onto every chunk unchanged —
    it describes the whole document, not an arbitrary slice of it.

    Documents at or under the limit pass through as a single chunk,
    unmodified, so this is a no-op for the common case.
    """
    chunked = []
    for doc in documents:
        content = doc.page_content or ""
        if len(content) <= _CHUNK_CHAR_LIMIT:
            chunked.append(doc)
            continue

        pieces = _split_on_paragraphs(content, _CHUNK_CHAR_LIMIT, _CHUNK_OVERLAP_CHARS)
        # parent_id + chunk_index is the contract collection._is_listed_point
        # (lists chunk_index 0 only) and llm/ranking.py (groups chunks by
        # parent) already read; without parent_id every chunk lists as its own
        # document and none of them group.
        parent_id = getattr(doc, "id", None) or hashlib.md5(content.encode()).hexdigest()
        for i, piece in enumerate(pieces):
            # Deep copy, not dict(): a shallow copy shares mutable values
            # (e.g. the `capabilities` list) across every chunk of this
            # document, so mutating one chunk's metadata would silently
            # mutate all the others.
            metadata = copy.deepcopy(doc.metadata) if doc.metadata else {}
            metadata.setdefault("parent_id", parent_id)
            metadata["chunk_index"] = i
            metadata["chunk_count"] = len(pieces)
            chunked.append(Document(page_content=piece, metadata=metadata))
    return chunked


def _split_on_paragraphs(text, limit, overlap):
    """Greedily pack paragraphs (blank-line-separated) into chunks up to
    `limit` chars, carrying the last `overlap` chars of each chunk into the
    next so a boundary doesn't strand a sentence with no context. A single
    paragraph longer than `limit` is hard-sliced rather than left oversized,
    since an oversized chunk defeats the whole point of chunking.
    """
    paragraphs = [p for p in text.split("\n\n") if p.strip()]
    chunks = []
    current = ""
    for para in paragraphs:
        candidate = f"{current}\n\n{para}" if current else para
        if len(candidate) <= limit:
            current = candidate
            continue
        if current:
            # A flush shorter than the overlap window is fully re-carried
            # into the next chunk's prefix below (current[-overlap:] is then
            # the whole string), so appending it here too would emit a
            # redundant near-duplicate chunk with nothing lost by skipping it.
            if len(current) > overlap:
                chunks.append(current)
            current = current[-overlap:] + "\n\n" + para if overlap else para
        else:
            current = para
        while len(current) > limit:
            chunks.append(current[:limit])
            # max(1, ...) rather than max(0, ...): guarantees forward
            # progress even if a future caller passes overlap >= limit,
            # where limit - overlap <= 0 would otherwise re-slice from 0
            # every time and loop forever.
            current = current[max(1, limit - overlap) :]
    if current:
        chunks.append(current)
    return chunks or [text[:limit]]


_KB_CHUNK_SHAPES = frozenset(
    {
        "deterministic_procedure",
        "decision_tree",
        "narrative_reference",
        "ui_walkthrough_business_app",
        "credential_access_procedure",
    }
)

# Every shape except narrative_reference has steps to follow — that's the
# sop/knowledge split the KB capability catalog is keyed on (see the
# llm_kb_capability_entries migration comment). Coarser than `shape`, but
# it's the field worth filtering RAG search results on directly.
_PROCEDURAL_KB_SHAPES = _KB_CHUNK_SHAPES - {"narrative_reference"}

_KB_METADATA_EXTRACTION_PROMPT = """Classify the overall shape of the following document, and identify which \
capabilities (if any) it references. A long document is shown as excerpts separated by "[...]"; classify \
the document as a whole.

Shape must be exactly one of:
- deterministic_procedure: numbered/ordered steps, each with a clear action verb and object
- decision_tree: explicit branches or conditions ("if X, do A; if Y, do B")
- narrative_reference: prose or background information with no imperative steps to follow
- ui_walkthrough_business_app: steps that involve logging into a named business application (CRM, \
billing, ticketing UI) to view customer-specific data
- credential_access_procedure: steps that exist only to establish an interactive, credentialed session \
(VPN+SSH, a web-app login, a DB client connection) rather than to diagnose or decide anything
{capability_section}
Respond with ONLY a JSON object of this exact shape, nothing else:
{{"shape": "<one of the shapes above>", "capabilities": [<capability names, or empty list>]}}

Document:
{content}"""

_CAPABILITY_SECTION_TEMPLATE = """
Capabilities referenced must be copied EXACTLY, verbatim, from this list — never invent a name that isn't here. \
If none of these apply, use an empty list:
{capability_names}
"""

# Bounds how many documents are classified concurrently per process_documents
# call — keeps ingestion off the sequential-call path without opening an
# unbounded number of provider connections at once.
_KB_METADATA_MAX_WORKERS = 5

# What the classifier is shown of one document. Over the cap, the document is
# represented by evenly spaced windows across its whole length (see
# _sample_for_classification) rather than its head.
_KB_CLASSIFY_MAX_CHARS = 12000
_KB_CLASSIFY_WINDOWS = 4
_KB_EXCERPT_SEPARATOR = "\n[...]\n"


def _extract_json_object(text):
    """Extract a JSON object from a response that may carry junk around it.

    Despite being told to respond with "ONLY a JSON object", real responses
    observed from this exact prompt have included a ```json fence and, in a
    separate case, a leading "---\\n\\n" separator before the object — two
    different quirks, so this takes the fully general approach (first '{' to
    last '}') rather than pattern-matching each preamble style as it's found.
    """
    start = text.find("{")
    end = text.rfind("}")
    if start == -1 or end == -1 or end < start:
        return text
    return text[start : end + 1]


def _sample_for_classification(text):
    """The text a document is classified by. Anything over
    _KB_CLASSIFY_MAX_CHARS is represented by evenly spaced windows across its
    whole length instead of its head: a long runbook often opens with an
    overview or table of contents, and classifying from the head alone would
    label the procedural pages after it as reference material.
    """
    if len(text) <= _KB_CLASSIFY_MAX_CHARS:
        return text
    window = _KB_CLASSIFY_MAX_CHARS // _KB_CLASSIFY_WINDOWS
    last_start = len(text) - window
    starts = [i * last_start // (_KB_CLASSIFY_WINDOWS - 1) for i in range(_KB_CLASSIFY_WINDOWS)]
    return _KB_EXCERPT_SEPARATOR.join(text[s : s + window] for s in starts)


def _document_text(group):
    return "\n\n".join(doc.page_content or "" for doc in group)


def _content_id(doc):
    """The id _generate_document_ids gives (or already gave) this document."""
    return getattr(doc, "id", None) or hashlib.md5(doc.page_content.encode()).hexdigest()


def _group_chunk_ids(group):
    """Normalised ids of every chunk a source document will be split into."""
    return {_normalize_point_id(_content_id(chunk)) for doc in group for chunk in _chunk_long_documents([doc])}


def _unclassified_groups(groups, classified_ids):
    """The groups still worth an LLM call: those with at least one chunk whose
    stored point carries no classification. A source document whose every chunk
    is already labelled is unchanged content on a re-sync — its chunks are
    skipped by the existence probe in process_all_batches, so reclassifying it
    would only spend money on a fresh, non-deterministic answer.
    """
    return [group for group in groups if not _group_chunk_ids(group) <= classified_ids]


def _classify_source_documents(groups, account_id, collection_name):
    """Classify each source document once and stamp the result onto every
    Document in its group — the labels then reach every chunk, because
    _chunk_long_documents copies a document's metadata onto its chunks. Per the
    SOP/runbook agentification design ("sop runbook agentification overview.md"
    §5, L0: doc -> shape) the unit is the source document, not the ~400-char
    fragment a chunk is: a fragment can't reveal whether a procedure is a
    decision tree or a narrative.

    `groups` is a list of lists: each inner list is ONE source document, given
    as the Documents that make up its text (usually one; a manual upload that
    was pre-split into lines or PDF chunks arrives as several).

    Classification is the derived sop/knowledge `kind` plus the real platform
    capabilities the document references, grounded against
    get_capability_catalog() (utils/db_query.py) — the shared table llm-server
    keeps in sync with its real tool/task registries. Every name the model
    returns is checked against that real list; anything not found is dropped,
    never trusted. An empty/unreachable catalog means capability extraction is
    skipped entirely (the prompt omits the capability section) rather than
    asking the model to guess against nothing.

    Best-effort, all-or-nothing per document: a classification failure, an
    unresolvable LLM provider, or a malformed/out-of-enum response leaves that
    document's chunks unclassified rather than storing anything unvalidated or
    failing the whole ingest. Nothing here retries; the next ingest or the
    startup backfill (kb_capability_backfill.py) picks the document up again.

    Callers decide what a source document is and whether the account may be
    classified at all (see _classify_kb_source_documents). A falsy account_id
    skips everything: there is no LLM provider to resolve without one.

    Returns how many documents were actually sent to the LLM (0 when nothing
    was: no account, no groups, or no resolvable provider), so a caller that
    budgets LLM calls only charges for ones that were made.
    """
    if not account_id:
        return 0
    # A document with no text has nothing to classify; a blank page would only
    # spend a call to be told so, on every sync.
    groups = [group for group in groups if _document_text(group).strip()]
    if not groups:
        return 0
    try:
        from rag.core.embeddings.generator import get_llm

        llm = get_llm(account_id)
    except Exception:
        logger.exception(f"kb_metadata: failed to resolve LLM provider for account {account_id}, skipping")
        return 0

    from rag.core.utils.db_query import get_capability_catalog

    valid_capabilities = {entry["name"] for entry in get_capability_catalog()}
    capability_section = ""
    if valid_capabilities:
        capability_section = _CAPABILITY_SECTION_TEMPLATE.format(
            capability_names="\n".join(f"- {name}" for name in sorted(valid_capabilities))
        )

    # Each call is one independent, I/O-bound LLM round-trip. Bounded
    # concurrency keeps this off any single request path while capping how many
    # calls hit the provider at once.
    batch_start = time.monotonic()
    with ThreadPoolExecutor(max_workers=_KB_METADATA_MAX_WORKERS) as executor:
        futures = [
            executor.submit(_classify_document_group, group, llm, capability_section, valid_capabilities)
            for group in groups
        ]
        results = [future.result() for future in futures]
    batch_duration = time.monotonic() - batch_start

    total_input_tokens = sum(r["input_tokens"] for r in results)
    total_output_tokens = sum(r["output_tokens"] for r in results)
    total_tokens = total_input_tokens + total_output_tokens
    succeeded = sum(1 for r in results if r["success"])
    failed = len(results) - succeeded
    logger.info(
        f"kb_metadata: classified {succeeded}/{len(results)} document(s) in {batch_duration:.2f}s wall-clock "
        f"({_KB_METADATA_MAX_WORKERS} workers) — tokens: input={total_input_tokens} output={total_output_tokens} "
        f"total={total_tokens}"
    )

    from rag.core.embeddings.tracker import persist_kb_classification_usage
    from utils.shared import get_provider_name

    persist_kb_classification_usage(
        account_id=account_id,
        collection_name=collection_name,
        provider=get_provider_name(type(llm).__name__),
        model=getattr(llm, "model", "") or "",
        total_tokens=total_tokens,
        document_count=len(results),
        duration_seconds=batch_duration,
        status="success" if failed == 0 else "partial",
        error_message=None if failed == 0 else f"{failed} of {len(results)} document(s) failed to classify",
    )
    return len(results)


def _classify_document_group(group, llm, capability_section, valid_capabilities):
    """Classify one source document and stamp its labels onto every Document in
    the group. Split out of _classify_source_documents so each document's LLM
    call can run on its own worker thread; errors are caught here so one
    failure never cancels the others' already-submitted work.

    Returns a per-call stats dict (duration_seconds, input_tokens,
    output_tokens, success) rather than persisting anything itself — the
    caller aggregates every document's stats into one usage row per call.
    """
    raw = None
    call_start = time.monotonic()
    stats = {"duration_seconds": 0.0, "input_tokens": 0, "output_tokens": 0, "success": False}
    try:
        prompt = _KB_METADATA_EXTRACTION_PROMPT.format(
            capability_section=capability_section, content=_sample_for_classification(_document_text(group))
        )
        result = llm.generate(prompt)
        stats["input_tokens"] = result.input_tokens or 0
        stats["output_tokens"] = result.output_tokens or 0
        raw = result.text.strip()
        parsed = json.loads(_extract_json_object(raw))
        shape = str(parsed.get("shape", "")).strip().lower()
        capabilities = parsed.get("capabilities", [])
    except Exception:
        stats["duration_seconds"] = time.monotonic() - call_start
        # Log the raw response, not just the traceback: a JSONDecodeError
        # alone doesn't say whether the model returned nothing, prose, or
        # markdown-fenced JSON — without this a parse failure is a black
        # box with no way to tell which.
        logger.exception(
            f"kb_metadata: document classification call failed after {stats['duration_seconds']:.2f}s, "
            f"leaving document unclassified; raw response: {raw!r:.500}"
        )
        return stats

    stats["duration_seconds"] = time.monotonic() - call_start
    stats["success"] = True

    labels = {}
    if shape in _KB_CHUNK_SHAPES:
        labels["shape"] = shape
        labels["kind"] = "sop" if shape in _PROCEDURAL_KB_SHAPES else "knowledge"
    else:
        logger.warning(f"kb_metadata: model returned an unrecognized shape {shape!r}, leaving document unclassified")

    if labels:
        confirmed = []
        if isinstance(capabilities, list):
            confirmed = [c for c in capabilities if c in valid_capabilities]
            dropped = [c for c in capabilities if c not in valid_capabilities]
            if dropped:
                logger.warning(
                    f"kb_metadata: dropped {len(dropped)} capability name(s) not in the real catalog: {dropped}"
                )
        labels["capabilities"] = confirmed
        for doc in group:
            if doc.metadata is None:
                doc.metadata = {}
            doc.metadata.update(copy.deepcopy(labels))

    logger.info(
        f"kb_metadata: classified document in {stats['duration_seconds']:.2f}s "
        f"(input_tokens={stats['input_tokens']}, output_tokens={stats['output_tokens']}, shape={shape!r}, "
        f"pieces={len(group)})"
    )
    return stats


# The labels a classified chunk carries under its vector payload's `metadata`
# (what RAG's metadata_filter matches at query time), and the kinds that make a
# chunk count as classified.
KB_LABEL_KEYS = ("kind", "shape", "capabilities")
_KB_KINDS = ("sop", "knowledge")
_LABEL_PAYLOAD_KEYS = [f"metadata.{key}" for key in KB_LABEL_KEYS]
_LABEL_WRITE_BATCH = 500
_SUMMARY_SCROLL_BATCH = 1000


def _point_id(chunk_id):
    """The canonical UUID form Qdrant returns for a chunk's bare-hex id."""
    try:
        return str(uuid.UUID(str(chunk_id)))
    except ValueError:
        return str(chunk_id)


def _classified_labels(collection_name, ids):
    """{normalised chunk id: labels} for the chunks of `ids` that are stored in
    the collection WITH a classification. This is the one and only "already
    classified" marker: the labels live on the chunk's own vector payload, so a
    chunk that was never stored, or was re-upserted without its labels, is
    simply not in the answer and gets classified again. A missing collection is
    an empty answer; any other error is raised for the caller to decide.
    """
    if not ids:
        return {}
    client = get_qdrant_client()
    if not client.collection_exists(collection_name):
        return {}
    ids = list(ids)
    labels = {}
    for start in range(0, len(ids), _EXISTENCE_PROBE_CHUNK):
        found = client.retrieve(
            collection_name=collection_name,
            ids=ids[start : start + _EXISTENCE_PROBE_CHUNK],
            with_payload=_LABEL_PAYLOAD_KEYS,
            with_vectors=False,
        )
        for point in found:
            metadata = (point.payload or {}).get("metadata") or {}
            if metadata.get("kind") in _KB_KINDS:
                labels[_normalize_point_id(point.id)] = {k: metadata[k] for k in KB_LABEL_KEYS if k in metadata}
    return labels


def _classify_kb_source_documents(documents, module, account_id, collection_name, whole_call_is_one_document):
    """Run the document-level classification for one process_documents call and
    return the labels the collection already stores for these chunks
    ({normalised chunk id: labels}), so the caller can carry them onto chunks
    this run did not classify.

    Only knowledge-base ingests are classified: process_documents is also the
    entry point for unrelated loaders (observability, database, account) that
    pass a real account_id, and classifying their content would spend LLM calls
    on things that are not knowledge-base documents.

    A manual upload (`whole_call_is_one_document`, i.e. it carries a
    knowledgebase_id) is ONE document however its loader split it — into lines,
    records or PDF chunks — so the whole list is classified together. Anything
    else (Confluence pages, ServiceNow articles) is a list of independent
    documents, each classified on its own.

    Known gap: the Confluence and ServiceNow scrapers pass a tenant_id and no
    account_id, and an LLM provider cannot be resolved without one, so nothing
    is classified for them here — integration KBs are labelled only by the
    startup backfill until the scrapers resolve an account to classify with. The
    stored labels are still read, so a sync never strips the backfill's labels.
    """
    if module != "knowledge_base":
        return {}
    groups = [documents] if whole_call_is_one_document else [[doc] for doc in documents]
    ids = set().union(*(_group_chunk_ids(group) for group in groups)) if groups else set()
    try:
        stored_labels = _classified_labels(collection_name, ids)
    except Exception:
        # Fail open: at worst everything is classified again.
        logger.exception(f"kb_metadata: could not read the stored classifications of {collection_name}")
        stored_labels = {}
    if account_id:
        _classify_source_documents(_unclassified_groups(groups, stored_labels.keys()), account_id, collection_name)
    return stored_labels


def _carry_over_labels(documents, stored_labels):
    """Put a chunk's stored labels back on it when this run did not classify it.

    An upsert replaces a point's whole payload with the document's metadata, so
    any path that re-embeds an already-classified chunk (the existence probe
    failing open, a metadata-cache miss turning verification off, an integration
    sync that classifies nothing) would silently strip its labels. Re-stamping
    them makes every upsert label-preserving.
    """
    if not stored_labels:
        return
    for doc in documents:
        if doc.metadata and "kind" in doc.metadata:
            continue
        stored = stored_labels.get(_normalize_point_id(_content_id(doc)))
        if stored:
            if doc.metadata is None:
                doc.metadata = {}
            doc.metadata.update(copy.deepcopy(stored))


def _stored_point_ids(collection_name, ids):
    """Normalised ids of `ids` already stored in the collection. Empty when the
    collection does not exist yet (a first load creates it later) or when the
    probe fails: the caller only uses this to decide which chunks need their
    payload updated in place, and updating nothing is always safe — an unlabelled
    chunk is classified again on the next run."""
    if not ids:
        return frozenset()
    try:
        if not get_qdrant_client().collection_exists(collection_name):
            return frozenset()
        return frozenset(_existing_point_ids(collection_name, list(ids)))
    except Exception:
        logger.exception(f"kb_metadata: could not tell which chunks are already stored in {collection_name}")
        return frozenset()


def _write_label_batch(client, collection_name, labels, point_ids):
    """Label one batch of already-stored chunks in place. Each batch is its own
    unit: a request Qdrant rejects (one id in it was pruned in the meantime makes
    the whole request a 404) must not stop the batches after it. If the write
    fails, the chunks would still carry whatever label they had before and count
    as classified, so a document that was just re-classified would keep a mix of
    old and new labels for good; dropping their `kind` makes them unclassified
    again, which is what gets their document classified on the next run.
    """
    try:
        client.set_payload(collection_name=collection_name, payload=labels, points=point_ids, key="metadata", wait=True)
    except Exception:
        logger.exception(f"kb_metadata: could not label {len(point_ids)} already-stored chunk(s) of {collection_name}")
        try:
            client.delete_payload(collection_name=collection_name, keys=["metadata.kind"], points=point_ids, wait=True)
        except Exception:
            logger.exception(f"kb_metadata: could not clear the stale kind of those chunks in {collection_name}")


def _publish_kb_labels(fresh, collection_name, stored_before):
    """After the embed step: make the labels this run computed real, then refresh
    the collection's rollup.

    The embed step skips a chunk that is already stored, so the payload of one
    that pre-dates this run — embedded unlabelled after an earlier classification
    failure, or carrying the labels of an older version of its document — never
    receives the labels just computed for it. They are written in place here, so
    a re-classified document ends up with one consistent classification on all of
    its chunks. Chunks embedded by this run already carry theirs.

    Best-effort, and a failure never leaves a stale label counted as classified
    (see _write_label_batch).
    """
    if not fresh:
        return
    by_labels = {}
    for doc in fresh:
        chunk_id = _normalize_point_id(_content_id(doc))
        if chunk_id in stored_before:
            labels = {key: doc.metadata[key] for key in KB_LABEL_KEYS if key in doc.metadata}
            by_labels.setdefault(json.dumps(labels, sort_keys=True), (labels, []))[1].append(_point_id(chunk_id))
    if by_labels:
        try:
            client = get_qdrant_client()
            for labels, point_ids in by_labels.values():
                for start in range(0, len(point_ids), _LABEL_WRITE_BATCH):
                    _write_label_batch(client, collection_name, labels, point_ids[start : start + _LABEL_WRITE_BATCH])
        except Exception:
            logger.exception(f"kb_metadata: could not label the already-stored chunks of {collection_name}")
    refresh_kb_collection_summary(collection_name)


def refresh_kb_collection_summary(collection_name):
    """Recompute a collection's rollup row of llm_kb_capability_entries — every
    kind, shape and capability its labelled chunks carry — from the labels in its
    vector payload, which is the source of truth. The row is only what the
    Knowledge Base tab reads; nothing decides from it whether a chunk is
    classified.

    Called after ingest labels chunks, after a prune removes some, and by the
    backfill. Best-effort: a failure is logged and leaves the previous row, which
    is stale, never wrong about what is classified.
    """
    try:
        client = get_qdrant_client()
        kinds, shapes, capabilities = set(), set(), set()
        offset = None
        while True:
            batch, offset = client.scroll(
                collection_name=collection_name,
                limit=_SUMMARY_SCROLL_BATCH,
                offset=offset,
                with_payload=_LABEL_PAYLOAD_KEYS,
                with_vectors=False,
            )
            for point in batch:
                metadata = (point.payload or {}).get("metadata") or {}
                if metadata.get("kind") not in _KB_KINDS:
                    continue
                kinds.add(metadata["kind"])
                shape = metadata.get("shape")
                if isinstance(shape, str) and shape in _KB_CHUNK_SHAPES:
                    shapes.add(shape)
                if isinstance(metadata.get("capabilities"), list):
                    capabilities.update(c for c in metadata["capabilities"] if isinstance(c, str))
            if offset is None or not batch:
                break

        from rag.core.utils.db_query import set_kb_collection_summary

        set_kb_collection_summary(collection_name, sorted(kinds), sorted(shapes), sorted(capabilities))
    except Exception:
        logger.exception(f"kb_metadata: could not refresh the classification summary of {collection_name}")


def _forget_kb_collection_summary(collection_name):
    """Drop a deleted collection's rollup row (best-effort, like every write to it)."""
    from rag.core.utils.db_query import delete_kb_capabilities

    delete_kb_capabilities(collection_name)


def _stamp_provenance(documents, source=None, tenant_id=None):
    """Stamp provenance tags onto each document's metadata so per-source and
    per-tenant filters work at query time. Respects caller overrides.

    Deliberately no run id: an unchanged document is skipped before upsert, so the marker
    would only ever land on documents that changed — present on some points, absent on
    others, for reasons a reader cannot see. Which documents a load captured is answered
    from the source store, which writes every document it was given.
    """
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
                    _forget_kb_collection_summary(collection_name)
                    logger.info(f"Deleted collection: {collection_name}")
            else:
                logger.info("Collection metadata is empty. Deleting the existing collection before proceeding.")
                client = get_qdrant_client()
                client.delete_collection(collection_name)
                _forget_kb_collection_summary(collection_name)
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
    warning=None,
    run_id=None,
    source_document_count=None,
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

    run_id = run_id or str(uuid.uuid4())
    _stamp_provenance(documents, source=source, tenant_id=tenant_id)
    # Counted before chunking: a KB's document_count is what the list column
    # shows and what the documents drilldown enumerates (one row per parent,
    # collection._is_listed_point), so it stays in source-document units. The
    # chunks below are embedding and retrieval units, and the tracker's own
    # counts stay in those.
    # How many documents a person would say this load covered. Usually one per item
    # handed in, but a caller that has already split one thing into several — a PDF
    # broken into passages, a note split per line — passes the real figure, so the
    # knowledge base does not report a six-page file as eight documents.
    if source_document_count is None:
        source_document_count = len(documents)
    # The load ledger is a separate unit and has to stay one. Its own document_count
    # counts what was embedded, so the figure it is compared against must count what
    # was submitted for embedding — mixing a source count into that pair makes the
    # history render "8 / 1" and claim a completed load dropped seven documents.
    submitted_document_count = len(documents)
    # Classification runs on the source documents, BEFORE chunking, so a
    # document's labels ride onto each of its chunks through the metadata copy
    # _chunk_long_documents makes. Only a manual upload passes a
    # knowledgebase_id, and it is one document however its loader split it
    # (lines, records, PDF chunks); everything else is a list of independent
    # documents.
    stored_labels = _classify_kb_source_documents(
        documents, module, account_id, collection_name, whole_call_is_one_document=bool(knowledgebase_id)
    )
    # Chunk ids hash page_content only, so the labels stamped above cannot
    # change them.
    documents = _chunk_long_documents(documents)
    # The chunks this run classified, before stored labels are carried onto the
    # rest (see _carry_over_labels).
    # Knowledge-base ingests only: other loaders stamp their own `kind` (memory records do), and
    # publishing those would scroll a whole collection to refresh a rollup for every write.
    fresh_labels = (
        [doc for doc in documents if doc.metadata and doc.metadata.get("kind") in _KB_KINDS]
        if module == "knowledge_base"
        else []
    )
    _carry_over_labels(documents, stored_labels)

    # Initialize tracker with load metadata
    tracker = EmbeddingTracker(
        account_id,
        collection_name,
        knowledgebase_id=knowledgebase_id,
        triggered_by=triggered_by,
        trigger_type=trigger_type,
        module=module,
        expected_document_count=submitted_document_count,
        run_id=run_id,
    )
    tracker.start_timer()

    # Generate document IDs
    documents_id = _generate_document_ids(documents)
    # Which freshly labelled chunks are already stored, before the embed step
    # runs: it skips those, so their payload has to be labelled in place after.
    stored_before = _stored_point_ids(collection_name, [_content_id(doc) for doc in fresh_labels])

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
        _publish_kb_labels(fresh_labels, collection_name, stored_before)
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
        update_kb_load_result(
            knowledgebase_id,
            "active",
            source_document_count,
            warning=warning,
        )

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
    """Helper function to identify documents that need to be deleted using direct Qdrant client.

    Returns point ids exactly as Qdrant returned them. Legacy points carry
    integer ids; stringifying one makes Qdrant reject the later delete with a
    400, so those points were never pruned.
    """
    try:
        # Scroll through all points to get existing IDs
        existing_ids = set()
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
            existing_ids.update(point.id for point in points)
            if next_offset is None:
                break
            offset = next_offset

        logger.info(f"Length of documents processed: {len(documents_id)}")
        logger.info(f"Length of existing documents: {len(existing_ids)}")
        normalized_doc_ids = {_normalize_point_id(doc_id) for doc_id in documents_id}
        missing_ids = {eid for eid in existing_ids if _normalize_point_id(eid) not in normalized_doc_ids}
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

        if is_documents_updated:
            # A prune only ever removes chunks, so a collection with no rollup
            # row has nothing to correct.
            from rag.core.utils.db_query import has_kb_collection_summary

            if has_kb_collection_summary(collection_name):
                refresh_kb_collection_summary(collection_name)

        gc.collect()

    except Exception as e:
        if is_not_found_error(e):
            logger.info(f"Collection {collection_name} does not exist. Ignoring missing document deletion.")
        else:
            logger.error(f"Error while fetching collection: {str(e)}")
            raise

    return is_documents_updated
