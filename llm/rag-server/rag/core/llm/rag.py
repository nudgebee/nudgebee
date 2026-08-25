import json
import logging
import re
import time
from typing import Dict, Optional

from rag.core.types import Document, LLM

from rag.core.embeddings.generator import get_llm
from rag.core.llm import circuit_breaker
from rag.core.llm.prompts import get_prompt_for_module
from rag.core.utils.db_query import get_live_kb_collection_names, get_tenant_id_for_account
from rag.qdrant.client import list_collections_optimized
from utils.config import Config
from utils.shared import get_provider_name

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


def _retrieve_documents_from_collections(
    collection_names, query, min_results, account_id, metadata_filter: Optional[Dict] = None
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
    for item in serializable_results:
        doc = Document(page_content=item["page_content"], metadata=item["metadata"])
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
            collection_names, query, no_of_results, account_id, metadata_filter
        )
        if not similar_docs_flat:
            return [], {}

        # Sort by relevance score
        similar_docs_flat.sort(key=lambda x: x[1], reverse=True)

        if use_reranking:
            # Cap docs sent to LLM to reduce input tokens and latency.
            # Only send the top candidates by similarity for reranking.
            max_docs_for_reranking = max(no_of_results * 2, Config.reranking_max_docs)
            docs_for_reranking = similar_docs_flat[:max_docs_for_reranking]
            if len(similar_docs_flat) > max_docs_for_reranking:
                logger.info(f"Capping docs for LLM reranking: {len(similar_docs_flat)} -> {max_docs_for_reranking}")

            llm = get_llm(account_id)
            docs_reranked, token_usage = rerank_with_llm(query, module, docs_for_reranking, llm)
        else:
            logger.info("Skipping LLM reranking (use_reranking=False)")
            docs_reranked = similar_docs_flat

        logger.info(f"Request complete - Returned {len(docs_reranked[:no_of_results])} docs")
        return docs_reranked[:no_of_results], token_usage
    except Exception as e:
        logger.error(f"Error in get_matching_documents: {str(e)}")
        return [], token_usage


def parse_ranking(response: str) -> list[float]:
    """
    Extracts the relevance scores from the LLM response in dict format.
    The response should be a JSON object where keys are string indices and values are floats.
    Returns a list of floats ordered by index.
    """
    try:
        # Remove any extra text before/after the JSON dict
        match = re.search(r"\{.*}", response, re.DOTALL)
        if not match:
            logger.warning("No JSON object found in the response.")
            return []

        json_obj = json.loads(match.group(0))

        if isinstance(json_obj, dict) and all(isinstance(v, (int, float)) for v in json_obj.values()):
            # Sort by int keys to maintain order
            return [float(json_obj[str(i)]) for i in sorted(map(int, json_obj.keys()))]

        logger.warning("Parsed JSON object is not a valid dictionary of scores.")
        return []
    except Exception as e:
        logger.warning(f"Error parsing JSON response: {e}")
        return []


def _adjust_scores_to_match_docs(scores: list, num_docs: int) -> list:
    """Adjust scores list to match the number of documents."""
    if isinstance(scores, list) and len(scores) != num_docs:
        logger.info("Adjusting the response to match the number of documents")
        scores.extend([0] * (num_docs - len(scores)))
    elif not isinstance(scores, list):
        scores = [0] * num_docs
    logger.info(f"Adjusted scores: {scores}")
    return scores


def _calculate_weighted_scores(docs: list, scores: list) -> list:
    """Calculate weighted scores combining similarity and LLM scores."""
    similarity_scores = [doc[1] for doc in docs]
    logger.info(f"Similarity scores: {similarity_scores}")
    logger.info(f"LLM scores: {scores}")

    # Calculate final scores with weight of 0.4 for similarity and 0.6 for LLM
    weighted_scores = [0.4 * sim + 0.6 * score for sim, score in zip(similarity_scores, scores)]
    logger.info(f"Final weighted scores: {weighted_scores}")

    scored_docs = [(docs[i], score) for i, score in enumerate(weighted_scores) if isinstance(score, (float, int))]
    logger.info(
        "Document ranking results: "
        f"{' | '.join([f'DocumentId: {doc[0].id} , Score: {score}' for doc, score in scored_docs])}"
    )

    return sorted(scored_docs, key=lambda x: -x[1])


def _apply_threshold_filter(ranked_docs: list, threshold: float) -> list:
    """Apply threshold filtering to remove low-scoring documents."""
    if threshold <= 0:
        return ranked_docs

    original_count = len(ranked_docs)
    filtered_docs = [doc_score for doc_score in ranked_docs if doc_score[1] >= threshold]
    filtered_count = len(filtered_docs)

    logger.info(
        f"Threshold filtering applied: Removed {original_count - filtered_count} documents with scores below "
        f"{threshold} (kept {filtered_count} out of {original_count} documents)"
    )

    return filtered_docs


def rerank_with_llm(user_question: str, module: str | None, docs: list, llm: LLM) -> tuple[list, dict]:
    """
    Given a user question and a list of documents, ask the LLM to rank them
    in order of relevance and return the re-ordered list along with token usage metadata.

    Returns:
        tuple: (ranked_documents, token_usage_metadata)
    """
    logger.info(f"Reranking documents with LLM for module {module} and question: {user_question}")
    prompt_template = get_prompt_for_module(module)
    formatted_docs = "\n".join([f"[{i}] {doc[0].page_content}" for i, doc in enumerate(docs)])
    llm_input = prompt_template.format(question=user_question, documents=formatted_docs)

    # Initialize token usage metadata
    token_usage = {
        "input_tokens": 0,
        "output_tokens": 0,
        "llm_provider": get_provider_name(llm.__class__.__name__),
        "llm_model": llm.model,
        "content_length": 0,
        "stop_reason": "FinishReasonStop",
    }

    # Fail fast if the per-pod breaker is open — the endpoint is known not-ready, so skip
    # the LLM call and return the docs unranked rather than hanging on client retries.
    cb_provider = get_provider_name(llm.__class__.__name__)
    cb_model = llm.model
    if circuit_breaker.is_open(cb_provider, cb_model):
        logger.warning("rerank: circuit open for %s/%s — skipping LLM rerank", cb_provider, cb_model)
        token_usage["request_status"] = "circuit_open"
        return docs, token_usage

    start_time = time.time()

    try:
        result = llm.generate(llm_input)
        circuit_breaker.record_success(cb_provider, cb_model)
        latency = time.time() - start_time

        response_content = result.text
        token_usage["input_tokens"] = result.input_tokens
        token_usage["output_tokens"] = result.output_tokens
        token_usage["content_length"] = len(response_content)
        token_usage["latency_seconds"] = latency
        token_usage["request_status"] = "success"

        # Clean and parse the response
        response_content = re.sub(r"\s+", " ", response_content)
        scores = parse_ranking(response_content)

        if not isinstance(scores, list) or len(scores) != len(docs):
            logger.warning(f"LLM response is not a valid list: {response_content}")
            scores = _adjust_scores_to_match_docs(scores, len(docs))

        # If all scores are 0.0, return the original order
        if all(score == 0 for score in scores):
            return docs, token_usage

        # Calculate weighted scores and rank documents
        ranked_docs_with_scores = _calculate_weighted_scores(docs, scores)

        # Apply threshold filtering
        ranked_docs_with_scores = _apply_threshold_filter(ranked_docs_with_scores, Config.ranking_threshold)

        logger.info(
            f"[TokenUsage] Rerank LLM call - input: {token_usage['input_tokens']},"
            f" output: {token_usage['output_tokens']}"
        )
        return [(doc[0], score) for doc, score in ranked_docs_with_scores], token_usage

    except Exception as e:
        if circuit_breaker.is_tripping_error(e):
            circuit_breaker.record_failure(cb_provider, cb_model)
        logger.warning(f"LLM failed to rank documents: {e}, using original order")
        token_usage["request_status"] = "failure"
        token_usage["error_message"] = str(e)
        return docs, token_usage
