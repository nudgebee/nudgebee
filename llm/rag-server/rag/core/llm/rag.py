import logging
from typing import Dict, Optional

from rag.core.types import Document

from rag.core.utils.db_query import get_live_kb_collection_names, get_tenant_id_for_account
from rag.qdrant.client import list_collections_optimized
from rag.core.llm import local_reranker
from utils.config import Config

logger = logging.getLogger(__name__)

# Collection-name shapes owned by an ``llm_knowledgebases`` row. Anything else
# (``nudgebee_docs``, per-module collections, …) is not KB-backed and is never
# subject to the live-KB check.
_MANUAL_KB_PREFIX = "kb_"
_INTEGRATION_KB_SUFFIX = "_knowledge_base"


def _is_kb_backed_collection(collection, tenant_id):
    """True when a collection is attributable to an ``llm_knowledgebases`` row.

    Only attributable collections may be dropped — an unrecognised one is kept,
    so we never silently stop searching data we cannot account for.

    ``<uuid>_knowledge_base`` is shared by three writers, and two of them are
    *not* integration KBs:

    - the tenant-level user KB, ``<tenant_id>_knowledge_base``
      (``loaders.account.load_tenant_knowledge_base_docs``); and
    - legacy per-account collections renamed from ``<account_id>_docs`` by
      ``module_retag_migration``, which carry an ``account`` tag because they
      predate tenant scoping.

    Both are excluded here; what remains under that suffix is integration-owned.
    """
    name = collection.name
    if name.startswith(_MANUAL_KB_PREFIX):
        return True
    if not name.endswith(_INTEGRATION_KB_SUFFIX):
        return False
    owner = name[: -len(_INTEGRATION_KB_SUFFIX)]
    if tenant_id and owner == str(tenant_id):
        return False
    return not (collection.metadata or {}).get("account")


def _drop_dead_kb_collections(collections, account_id, tenant_id):
    """Return the names of ``collections``, minus the dead knowledge-base ones.

    A collection is dropped only when it is KB-attributable *and* absent from
    the account's live set — i.e. its knowledge base was archived, or its
    integration was disabled or deleted (deleting an integration cascades the
    KB row away, stranding the vector collection). Without this, every search
    keeps reading collections whose integration no longer exists, which
    multiplies duplicate copies of the same page across the result set.

    Storage cleanup is a separate concern: dropping at read time means
    correctness does not wait on a Qdrant sweep.
    """
    kb_backed = {collection.name for collection in collections if _is_kb_backed_collection(collection, tenant_id)}
    if not kb_backed:
        return [collection.name for collection in collections]

    live_names = get_live_kb_collection_names(account_id, tenant_id)
    if live_names is None:
        logger.warning(
            "Could not resolve live knowledge bases for account %s / tenant %s - "
            "searching all %d matched collections",
            account_id,
            tenant_id,
            len(collections),
        )
        return [collection.name for collection in collections]

    kept, dropped = [], []
    for collection in collections:
        if collection.name in kb_backed and collection.name not in live_names:
            dropped.append(collection.name)
        else:
            kept.append(collection.name)

    if dropped:
        logger.info(f"Skipping {len(dropped)} collections with no live knowledge base: {dropped}")
    return kept


def _filter_collections_for_module_and_account(collections, module, account_id, collection_name, tenant_id=None):
    """Filter collections by module + visibility scope.

    A collection is included when its ``module`` tag matches AND at least one
    visibility predicate holds:

    - ``account`` matches the requested ``account_id`` (legacy per-account KBs).
    - ``account`` equals ``"global"`` (shared resources like product docs).
    - ``tenant_id`` matches the requested ``tenant_id`` (tenant-scoped
      integration collections — Confluence/ServiceNow landed here after the
      per-tenant scrape refactor).

    The tenant path lets tenant-level integrations surface for every cloud
    account in the tenant without listing every account on the collection.

    Visible is not the same as live: a matched collection is dropped again by
    ``_drop_dead_kb_collections`` when its knowledge base is gone. An explicitly
    requested ``collection_name`` bypasses both passes — the caller named it.
    """
    matched = []
    for collection in collections:
        metadata = collection.metadata
        if not metadata:
            continue

        if metadata.get("module") != module:
            continue

        is_matching_account = metadata.get("account") == account_id if account_id else False
        is_global_account = metadata.get("account") == "global"
        is_matching_tenant = (
            tenant_id is not None and metadata.get("tenant_id") is not None and metadata["tenant_id"] == str(tenant_id)
        )

        if is_matching_account or is_global_account or is_matching_tenant:
            matched.append(collection)

    collection_names = _drop_dead_kb_collections(matched, account_id, tenant_id)
    if collection_name and collection_name not in collection_names:
        collection_names.append(collection_name)
    return collection_names


def _collection_scope(metadata):
    """Classify a collection from its own metadata, not from its name.

    Qdrant collection metadata is the authority on what a collection holds:

    - ``account == "global"``     -> shared content with no knowledge-base row
      (NudgeBee product docs). Callers must not expect to attribute it to a KB.
    - ``source == "user_kb"``     -> a manual knowledge base
    - ``source in (confluence, servicenow, ...)`` -> a synced integration KB
    - ``module``                  -> which subsystem owns it (knowledge_base,
      prometheus, logs, ...), so non-KB agent collections are distinguishable.

    Returned verbatim to the caller so llm-server does not have to re-derive any
    of this by parsing collection names or matching document text.
    """
    metadata = metadata or {}
    scope = "account"
    if metadata.get("account") == "global":
        scope = "global"
    elif metadata.get("tenant_id"):
        scope = "tenant"
    return {
        "collection_scope": scope,
        "collection_module": metadata.get("module") or "",
        "collection_source": metadata.get("source") or "",
    }


def _collection_classification(collection, tenant_id):
    """Full classification of a collection: scope plus whether it is KB-backed.

    ``collection_kb_backed`` is the field callers must branch on. It reuses
    ``_is_kb_backed_collection`` - the same predicate ``_drop_dead_kb_collections``
    trusts - so "does an llm_knowledgebases row exist for this?" has ONE answer in
    the system. Three kinds of collection are NOT KB-backed and can never be
    attributed to a knowledge base: the global product-docs collection, the
    tenant-level user KB, and legacy per-account collections renamed from
    ``<account_id>_docs``. Callers that demand a KB row for those drop content
    that is legitimately un-owned.
    """
    out = _collection_scope(collection.metadata or {})
    out["collection_kb_backed"] = _is_kb_backed_collection(collection, tenant_id)
    return out


def _retrieve_documents_from_collections(
    collection_names,
    query,
    min_results,
    account_id,
    metadata_filter: Optional[Dict] = None,
    collection_metadata: Optional[Dict] = None,
):
    """
    Retrieves documents from collections by directly calling search logic.
    Works with Qdrant.
    """
    if not collection_names:
        return []

    logger.info(f"Searching {len(collection_names)} collections directly")
    from rag.search.search_logic import search_collections

    serializable_results = search_collections(collection_names, query, account_id, min_results, metadata_filter)

    # Reconstruct the Document objects
    all_docs = []
    collection_metadata = collection_metadata or {}
    for item in serializable_results:
        md = item["metadata"] or {}
        # Attach the owning collection's classification so the caller can tell a
        # global collection from a KB-backed one without guessing.
        source_collection = md.get("collection")
        if source_collection in collection_metadata:
            md = {**md, **collection_metadata[source_collection]}
        doc = Document(page_content=item["page_content"], metadata=md)
        score = item["score"]
        all_docs.append((doc, score))

    logger.info(f"Search complete: retrieved {len(all_docs)} total documents.")
    return all_docs


def get_matching_documents(
    query: str,
    no_of_results: int,
    account_id: str,
    module: Optional[str] = None,
    collection_name: Optional[str] = None,
    metadata_filter: Optional[Dict] = None,
    use_reranking: bool = False,
    tenant_id: Optional[str] = None,
):
    """
    Returns:
        tuple: (documents, token_usage_metadata) where token_usage_metadata contains LLM usage info
    """
    # Resolve tenant_id lazily from the account when the caller didn't supply
    # one — keeps tenant-scoped integration collections (Confluence, ServiceNow)
    # reachable from legacy callers that only know about cloud accounts.
    if tenant_id is None and account_id:
        tenant_id = get_tenant_id_for_account(account_id)

    logger.info(
        f"Getting matching documents for module {module}, account {account_id}, tenant {tenant_id} "
        f"with query: {query} and k: {no_of_results}"
    )

    token_usage: dict = {}

    try:
        # Find relevant collections
        logger.info(f"Listing collections for module {module}, account {account_id}, tenant {tenant_id}")
        collections = list_collections_optimized()
        collection_names = _filter_collections_for_module_and_account(
            collections, module, account_id, collection_name, tenant_id=tenant_id
        )
        logger.info(
            f"Found {len(collection_names)} collections for module {module}, account {account_id}, "
            f"tenant {tenant_id}, collections: {collection_names}"
        )
        if not collection_names:
            return [], {}

        # Retrieve documents from collections using multiprocessing
        logger.info(f"Retrieving documents from collections: {collection_names}")
        similar_docs_flat = _retrieve_documents_from_collections(
            collection_names,
            query,
            no_of_results,
            account_id,
            metadata_filter,
            collection_metadata={c.name: _collection_classification(c, tenant_id) for c in collections},
        )
        if not similar_docs_flat:
            return [], {}

        # Sort by relevance score
        similar_docs_flat.sort(key=lambda x: x[1], reverse=True)

        if use_reranking:
            # Cap docs sent to the reranker to bound latency.
            max_docs_for_reranking = max(no_of_results * 2, Config.reranking_max_docs)
            docs_for_reranking = similar_docs_flat[:max_docs_for_reranking]
            if len(similar_docs_flat) > max_docs_for_reranking:
                logger.info(f"Capping docs for reranking: {len(similar_docs_flat)} -> {max_docs_for_reranking}")

            docs_reranked, ok = local_reranker.rerank(query, docs_for_reranking)
            if not ok:
                # Fail closed. Returning the unranked list here is what shipped
                # six irrelevant documents into a prompt: retrieval always
                # yields its nearest k, and without a working relevance score
                # nothing downstream can tell a good hit from a bad one.
                logger.warning("Reranking unavailable — returning no documents rather than unranked ones")
                return [], token_usage
        else:
            logger.info("Skipping reranking (use_reranking=False)")
            docs_reranked = similar_docs_flat

        logger.info(f"Request complete - Returned {len(docs_reranked[:no_of_results])} docs")
        return docs_reranked[:no_of_results], token_usage
    except Exception as e:
        logger.error(f"Error in get_matching_documents: {str(e)}")
        return [], token_usage
