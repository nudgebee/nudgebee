"""
Search controller.

Handles document search endpoints with token tracking and metadata filtering.
"""

import logging
from typing import Optional

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

from rag.core.llm.rag import get_matching_documents
from rag.core.monitoring.audit import save_audit_async
from rag.core.monitoring.token_tracker import persist_llm_token_usage
from utils.config import Config

router = APIRouter()
logger = logging.getLogger(__name__)


# Pydantic models
class GetMatchingDocRequest(BaseModel):
    query: str
    k: int = 1
    account_id: str
    module: Optional[str] = None
    collection_name: Optional[str] = None
    conversation_id: Optional[str] = None
    message_id: Optional[str] = None
    user_id: Optional[str] = None
    agent_id: Optional[str] = None
    track_token_usage: bool = True
    metadata_filter: Optional[dict] = None  # Optional metadata filtering
    # Optional tenant scope. When omitted, the server resolves it from
    # ``account_id`` so tenant-scoped integration collections (Confluence,
    # ServiceNow) are visible without the caller needing to know about tenants.
    tenant_id: Optional[str] = None
    use_reranking: bool = Config.reranking_enabled  # Enable LLM-based reranking (default from RAG_RERANKING_ENABLED)


def _validate_token_tracking(
    track_token_usage: bool,
    conversation_id: Optional[str],
    message_id: Optional[str],
) -> None:
    """Validate token tracking parameters."""
    # If track_token_usage is explicitly set to False in request, skip validation
    if not track_token_usage:
        return

    # When track_token_usage is True (default), conversation_id and message_id are required
    if not conversation_id or not message_id:
        raise HTTPException(
            status_code=400,
            detail=f"conversation_id and message_id are required when track_token_usage is True. "
            f"Got conversation_id={conversation_id}, message_id={message_id}",
        )


def _accumulate_token_usage(total_usage: dict, new_usage: dict) -> None:
    """Accumulate token usage metrics into total."""
    if not new_usage:
        return
    total_usage["input_tokens"] += new_usage.get("input_tokens", 0)
    total_usage["output_tokens"] += new_usage.get("output_tokens", 0)
    total_usage["content_length"] += new_usage.get("content_length", 0)
    total_usage["latency_seconds"] += new_usage.get("latency_seconds", 0)
    if not total_usage["llm_provider"]:
        total_usage["llm_provider"] = new_usage.get("llm_provider")
        total_usage["llm_model"] = new_usage.get("llm_model")
    if not total_usage.get("request_status"):
        total_usage["request_status"] = new_usage.get("request_status", "success")
        total_usage["error_message"] = new_usage.get("error_message")


def _persist_token_usage(
    track_token_usage: bool,
    token_usage: dict,
    conversation_id: Optional[str],
    message_id: Optional[str],
    agent_name: Optional[str],
    account_id: str,
    user_id: Optional[str],
    agent_id: Optional[str],
) -> None:
    """Log token usage to database."""
    if not track_token_usage or not token_usage or token_usage.get("input_tokens", 0) <= 0:
        return
    # conversation_id and message_id are guaranteed to be non-None when track_token_usage is True
    # (validated in _validate_token_tracking)
    if not conversation_id or not message_id:
        logger.warning("Missing conversation_id or message_id, skipping token usage logging")
        return
    try:
        persist_llm_token_usage(
            conversation_id=conversation_id,
            message_id=message_id,
            agent_name=agent_name or "unknown",
            account_id=account_id,
            token_usage=token_usage,
            user_id=user_id,
            agent_id=agent_id,
        )
    except Exception as log_error:
        logger.error(f"Failed to log token usage: {log_error}")


def _extract_prometheus_metric(doc_content: str) -> str:
    """Extract metric name from document content."""
    split = doc_content.split("\n")
    metric = split[0]
    if len(split) > 1:
        parts = split[1].split(":", 1)
        if len(parts) > 1:
            metric = parts[1].strip()
    return metric


def _fetch_prometheus_metadata(documents: list, account_id: str, total_token_usage: dict) -> list:
    """Fetch metadata for prometheus metrics and accumulate token usage."""
    metadata = []
    for doc, score in documents:
        if not doc.page_content:
            continue
        metric = _extract_prometheus_metric(doc.page_content)
        docs, metadata_token_usage = get_matching_documents(
            query=metric,
            no_of_results=1,
            account_id=account_id,
            module="prometheus-metadata",
        )
        _accumulate_token_usage(total_token_usage, metadata_token_usage)
        if docs:
            metadata.append(docs[0][0].page_content)
    return metadata


@router.post("/get_matching_doc")
async def get_matching_doc(request: GetMatchingDocRequest):
    """Get matching documents based on query with optional metadata filtering."""
    doc_response = []

    # Extract token tracking parameters and validate
    _validate_token_tracking(request.track_token_usage, request.conversation_id, request.message_id)

    try:
        logger.info(
            f"Getting matching document for account_id: {request.account_id}, "
            f"module: {request.module}, query: {request.query}, "
            f"metadata_filter: {request.metadata_filter}"
        )

        documents, token_usage = get_matching_documents(
            query=request.query,
            no_of_results=request.k,
            account_id=request.account_id,
            module=request.module,
            collection_name=request.collection_name,
            metadata_filter=request.metadata_filter,
            use_reranking=request.use_reranking,
            tenant_id=request.tenant_id,
        )

        for doc, score in documents:
            doc_response.append(
                {
                    "document": doc.page_content,
                    "metadata": doc.metadata,
                    "similarity_score": score,
                }
            )

        # Log token usage
        _persist_token_usage(
            request.track_token_usage,
            token_usage,
            request.conversation_id,
            request.message_id,
            request.module,
            request.account_id,
            request.user_id,
            request.agent_id,
        )

        save_audit_async(
            request.account_id,
            request.module,
            request.query,
            request.conversation_id,
            documents,
        )
    except Exception as e:
        logger.exception(f"Error getting matching document: {e}")
        raise HTTPException(status_code=500, detail=str(e))

    return doc_response if doc_response else []


@router.post("/get_prometheus_matching_doc")
async def get_prometheus_doc(request: GetMatchingDocRequest):
    """Get matching prometheus documents with metadata."""
    # Extract token tracking parameters and validate
    _validate_token_tracking(request.track_token_usage, request.conversation_id, request.message_id)

    total_token_usage = {
        "input_tokens": 0,
        "output_tokens": 0,
        "llm_provider": None,
        "llm_model": None,
        "content_length": 0,
        "latency_seconds": 0,
    }

    logger.info("Getting matching document")
    try:
        documents, token_usage = get_matching_documents(
            query=request.query,
            no_of_results=request.k,
            account_id=request.account_id,
            module=request.module,
            use_reranking=request.use_reranking,
        )

        # Accumulate token usage from main query
        _accumulate_token_usage(total_token_usage, token_usage)

        # Fetch metadata and accumulate token usage
        metadata = _fetch_prometheus_metadata(documents, request.account_id, total_token_usage)

        # Log accumulated token usage
        _persist_token_usage(
            request.track_token_usage,
            total_token_usage,
            request.conversation_id,
            request.message_id,
            request.module,
            request.account_id,
            request.user_id,
            request.agent_id,
        )

    except Exception as e:
        logger.exception(f"Error getting matching document df: {e}")
        return {"status": "error", "error": str(e)}

    content = [doc.page_content for doc, score in documents]
    if not content:
        return {"document": [], "metadata": []}
    return {"document": content, "metadata": metadata}
