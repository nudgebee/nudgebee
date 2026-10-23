"""Exact indexed knowledge reads; never re-run semantic search for a handle."""

import hashlib
import logging
from typing import Any, Iterator
from types import SimpleNamespace

from fastapi import APIRouter, HTTPException, Response
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, Field
from qdrant_client.models import FieldCondition, Filter, MatchValue

from rag.core.documents.collection import SOURCE_ID_KEYS, stored_source_for
from rag.core.documents.processing import _CHUNK_OVERLAP_CHARS
from rag.core.llm.rag import _filter_collections_for_module_and_account, _is_kb_backed_collection
from rag.core.utils.db_query import get_live_kb_collection_names, get_tenant_id_for_account
from rag.qdrant.client import get_qdrant_client

router = APIRouter()
logger = logging.getLogger(__name__)
MAX_DOCUMENT_BYTES = 32 * 1024 * 1024
# Nothing real comes near this many chunks of one page; a larger count is
# treated as corrupt rather than assembled.
MAX_ASSEMBLED_CHUNKS = 2048
_SCROLL_PAGE = 256
# Converting HTML is CPU and memory on the request thread (~0.25s per MB); a
# Confluence page body is far below this.
MAX_SOURCE_HTML_BYTES = 2 * 1024 * 1024


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
    # The scraper's own document id (SOURCE_ID_KEYS) is kept: llm-server keys a
    # document's Fact/SOP mark on it, and the Documents list reads the same id
    # from the point. Dropping it here left every hit keyed on its URL instead,
    # so no mark ever matched in this search mode.
    safe: dict[str, Any] = {
        key: str(metadata[key])[:1024]
        for key in ("title", "url", "source", "collection", "retrieval_id", "kb_id", "kb_name", "kind", *SOURCE_ID_KEYS)
        if metadata.get(key) is not None
    }
    # capabilities (#38623's classifier output) is a list, not a scalar — the
    # str() above would turn it into a Python repr string that llm-server's Go
    # client cannot parse back into a list, silently discarding it. Kept as a
    # real list; bounded defensively even though the catalog-grounding at
    # classification time already keeps it short.
    capabilities = metadata.get("capabilities")
    if isinstance(capabilities, list):
        safe["capabilities"] = [str(c)[:256] for c in capabilities[:32]]
    safe.update(content_sha256=version, content_bytes=size, completeness="indexed_document")
    return excerpt, safe


def join_chunks(chunks: list[str], overlap: int = _CHUNK_OVERLAP_CHARS) -> str | None:
    """Undo _chunk_long_documents: chunk k+1 starts with the last `overlap`
    characters of chunk k, so each join must find exactly that prefix. A
    missing match means the chunks are not a clean set (edited, re-chunked
    with another overlap, partially synced); return None rather than guess."""
    text = chunks[0]
    for nxt in chunks[1:]:
        carried = min(overlap, len(text))
        if carried and not nxt.startswith(text[-carried:]):
            return None
        text += nxt[carried:]
        if len(text) > MAX_DOCUMENT_BYTES:
            return None
    return text


def _chunk_body(index: int, text: str, overlap: int = _CHUNK_OVERLAP_CHARS) -> str:
    """The part of a chunk that is the page itself: everything but the carried overlap."""
    if index == 0:
        return text
    return text[overlap + 2 :] if text[overlap : overlap + 2] == "\n\n" else text[overlap:]


def _normalise(text: str) -> str:
    """The page as _split_on_paragraphs saw it: blank paragraphs dropped."""
    return "\n\n".join(p for p in text.split("\n\n") if p.strip())


def page_from_source(collection: str, metadata: dict, index: int, chunks: dict[int, str]) -> str | None:
    """Rebuild a Confluence page from its stored source when the chunk set is unclean.

    Chunk point ids hash the chunk text alone, so a chunk another page (or an
    older version of this one) shares is one point carrying whichever page
    wrote last, and its siblings go missing. The stored source is the same HTML
    the indexed text was converted from, so it reproduces the page exactly, but
    it is the newest stored version, which an edit can have moved past the
    chunk that matched. Accepted only when every chunk we still hold (the one
    asked for included) appears in it. `chunks` maps index to text and holds the
    asked-for chunk."""
    if metadata.get("source") != "confluence" or metadata.get("is_figure"):
        return None
    try:
        stored = stored_source_for(collection, metadata)
    except Exception:
        logger.warning("knowledge_document: could not read the stored source", exc_info=True)
        return None
    if not stored or len(stored["content"]) > MAX_SOURCE_HTML_BYTES or stored.get("content_type") != "text/html":
        return None
    html = stored["content"]
    from rag.core.documents.scraper import extract_content

    title = (metadata.get("title") or "").strip()
    try:
        body = extract_content(html)
    except Exception:  # e.g. RecursionError on pathologically nested markup
        logger.warning("knowledge_document: could not convert the stored source", exc_info=True)
        return None
    page = _normalise(f"Title: {title}\n\n{body}" if title else body)
    if any(_chunk_body(i, text) not in page for i, text in chunks.items()):
        return None
    return page


def assemble_document(client, collection: str, payload: dict) -> tuple[str | None, str]:
    """Rebuild the whole page a chunk belongs to: from its sibling chunks in the
    same collection, else from the page's stored source. Returns (text, "") or
    (None, reason); the caller falls back to the chunk itself on None."""
    metadata = payload.get("metadata") or {}
    parent_id, index, count = metadata.get("parent_id"), metadata.get("chunk_index"), metadata.get("chunk_count")
    if not parent_id or not isinstance(index, int) or not isinstance(count, int):
        return None, "not_chunked"
    if count <= 1:
        return None, "single_chunk"
    if count > MAX_ASSEMBLED_CHUNKS:
        return None, "too_many_chunks"
    texts: dict[int, str] = {}
    unclean = False
    gathered, offset = 0, None
    while True:
        points, offset = client.scroll(
            collection_name=collection,
            scroll_filter=Filter(must=[FieldCondition(key="metadata.parent_id", match=MatchValue(value=parent_id))]),
            limit=_SCROLL_PAGE,
            with_payload=["page_content", "metadata.chunk_index", "metadata.chunk_count"],
            with_vectors=False,
            offset=offset,
        )
        for point in points:
            gathered += 1
            body = point.payload or {}
            md = body.get("metadata") or {}
            i = md.get("chunk_index")
            # Siblings from another version of the page (an edit keeps unchanged
            # chunks' parent_id) disagree on count or repeat an index.
            if md.get("chunk_count") != count or not isinstance(i, int) or i in texts:
                unclean = True
                continue
            texts[i] = body.get("page_content") or ""
        if gathered > MAX_ASSEMBLED_CHUNKS:
            return None, "too_many_chunks"
        if offset is None:
            break
    reason = ""
    if unclean:
        reason = "inconsistent_siblings"
    elif sorted(texts) != list(range(count)):
        reason = "missing_chunks"
    elif texts[index] != payload.get("page_content"):
        reason = "inconsistent_siblings"
    else:
        text = join_chunks([texts[i] for i in range(count)])
        if text is not None:
            return text, ""
        reason = "overlap_mismatch"
    page = page_from_source(collection, metadata, index, {**texts, index: payload.get("page_content") or ""})
    if page is not None:
        logger.info("knowledge_document: rebuilt the page from its stored source (%s)", reason)
        return page, ""
    return None, reason


class KnowledgeDocumentRequest(BaseModel):
    account_id: str = Field(min_length=1, max_length=128)
    collection_name: str = Field(min_length=1, max_length=256)
    document_id: str = Field(min_length=1, max_length=128)
    content_sha256: str = Field(pattern=r"^[a-f0-9]{64}$")
    validate_only: bool = False
    # Opt-in: return the whole page the chunk belongs to instead of the chunk.
    # Old clients never send it, so they keep getting the chunk.
    whole_document: bool = False


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
        with_payload=[
            "page_content",
            "metadata.parent_id",
            "metadata.chunk_index",
            "metadata.chunk_count",
            "metadata.source",
            "metadata.title",
            "metadata.is_figure",
            *[f"metadata.{key}" for key in SOURCE_ID_KEYS],
        ],
        with_vectors=False,
    )
    if not points:
        raise HTTPException(404, "Knowledge document unavailable")
    payload = points[0].payload or {}
    content = payload.get("page_content", "")
    version, size = content_version(content)
    if version != request.content_sha256:
        raise HTTPException(409, "Knowledge document changed; search again")
    scope = "chunk"
    if request.whole_document and not request.validate_only:
        # Opt-in extra: whatever goes wrong here, the caller still gets the chunk.
        try:
            assembled, reason = assemble_document(client, request.collection_name, payload)
            if assembled is not None:
                assembled_version, assembled_size = content_version(assembled)
                content, version, size, scope = assembled, assembled_version, assembled_size, "document"
        except Exception:
            assembled, reason = None, "error"
            logger.warning("knowledge_document: whole-document read failed", exc_info=True)
        if assembled is None:
            logger.warning("knowledge_document: whole-document read fell back to the chunk (%s)", reason)
    headers = {"X-Content-SHA256": version, "X-Content-Bytes": str(size), "X-Content-Scope": scope}
    if request.validate_only:
        return Response(status_code=204, headers=headers)
    headers["Content-Length"] = str(size)
    return StreamingResponse(content_chunks(content), media_type="text/plain", headers=headers)
