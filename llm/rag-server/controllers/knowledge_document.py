"""Exact indexed knowledge reads; never re-run semantic search for a handle."""

import hashlib
from typing import Any, Iterator
from types import SimpleNamespace

from fastapi import APIRouter, HTTPException, Response
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, Field

from rag.core.llm.rag import _filter_collections_for_module_and_account, _is_kb_backed_collection
from rag.core.utils.db_query import get_live_kb_collection_names, get_tenant_id_for_account
from rag.qdrant.client import get_qdrant_client

router = APIRouter()
MAX_DOCUMENT_BYTES = 32 * 1024 * 1024


def content_chunks(text: str) -> Iterator[bytes]:
    for start in range(0, len(text), 16384):
        yield text[start : start + 16384].encode("utf-8")


def content_version(text: str) -> tuple[str, int]:
    digest = hashlib.sha256()
    size = 0
    for chunk in content_chunks(text):
        size += len(chunk)
        if size > MAX_DOCUMENT_BYTES:
            raise HTTPException(413, "Indexed document exceeds the transfer limit")
        digest.update(chunk)
    return digest.hexdigest(), size


def discovery_document(text: str, metadata: dict, limit: int) -> tuple[str, dict]:
    """Bound both text and metadata; source payload metadata can contain large bodies."""
    try:
        version, size = content_version(text)
    except HTTPException as exc:
        if exc.status_code != 413:
            raise
        # Keep other search results usable and expose a bounded candidate that
        # the loader can explicitly reject, instead of failing the whole search.
        version, size = "", MAX_DOCUMENT_BYTES + 1
    excerpt = text[:limit].encode("utf-8")[:limit].decode("utf-8", errors="ignore")
    safe: dict[str, Any] = {
        key: str(metadata[key])[:1024]
        for key in ("title", "url", "source", "collection", "retrieval_id", "kb_id", "kb_name")
        if metadata.get(key) is not None
    }
    safe.update(content_sha256=version, content_bytes=size, completeness="indexed_document")
    return excerpt, safe


class KnowledgeDocumentRequest(BaseModel):
    account_id: str = Field(min_length=1, max_length=128)
    collection_name: str = Field(min_length=1, max_length=256)
    document_id: str = Field(min_length=1, max_length=128)
    content_sha256: str = Field(pattern=r"^[a-f0-9]{64}$")
    validate_only: bool = False


@router.post("/knowledge/document")
def get_knowledge_document(request: KnowledgeDocumentRequest):
    tenant = get_tenant_id_for_account(request.account_id)
    # Exact reads must not inherit the discovery collection cache's stale grants.
    client = get_qdrant_client()
    try:
        info = client.get_collection(request.collection_name)
    except Exception as exc:
        raise HTTPException(503, "Cannot validate knowledge collection") from exc
    selected = [SimpleNamespace(name=request.collection_name, metadata=getattr(info.config, "metadata", None) or {})]
    allowed = _filter_collections_for_module_and_account(
        selected,
        "knowledge_base",
        request.account_id,
        request.collection_name,
        tenant_id=tenant,
        restrict_to_collection=True,
    )
    if request.collection_name not in allowed:
        raise HTTPException(404, "Knowledge document unavailable")
    if any(_is_kb_backed_collection(c, tenant) for c in selected):
        live = get_live_kb_collection_names(request.account_id, tenant)
        if live is None:
            raise HTTPException(503, "Cannot validate knowledge access")
        if request.collection_name not in live:
            raise HTTPException(404, "Knowledge document unavailable")
    doc_id = int(request.document_id) if request.document_id.isdecimal() else request.document_id
    points = client.retrieve(
        collection_name=request.collection_name,
        ids=[doc_id],
        with_payload=["page_content"],
        with_vectors=False,
    )
    if not points:
        raise HTTPException(404, "Knowledge document unavailable")
    content = (points[0].payload or {}).get("page_content", "")
    version, size = content_version(content)
    if version != request.content_sha256:
        raise HTTPException(409, "Knowledge document changed; search again")
    headers = {"X-Content-SHA256": version, "X-Content-Bytes": str(size)}
    if request.validate_only:
        return Response(status_code=204, headers=headers)
    headers["Content-Length"] = str(size)
    return StreamingResponse(content_chunks(content), media_type="text/plain", headers=headers)
