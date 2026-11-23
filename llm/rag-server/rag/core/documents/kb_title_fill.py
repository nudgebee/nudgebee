"""Put a ServiceNow page's title on every one of its chunks.

A ServiceNow article is indexed with its title only as the first line of its text
("Title: <short description>"), so only the first chunk of a page carries it. A search hit on
any other chunk has no title, which is why Nubi saw scrap titles like "mos DB accoun", and why
llm-server cannot tell from such a hit that the page is a runbook (its title rule reads the
title). The scraper now stamps ``metadata.title`` on new pages; this fills it into the pages
indexed before that.

Idempotent and cheap to repeat: it makes no LLM call and changes nothing once a page's chunks
agree. A page is settled by its first chunk: that chunk's own title, else the "Title:" line of
its text. Every chunk that has no title, or a different one, is given it. Different titles
happen when a page was renamed: an unchanged chunk keeps its id and is not re-written by the
scrape, so it keeps the old title while the chunks that changed carry the new one. A page
whose first chunk has neither a title nor a "Title:" line is left alone unless some of its
chunks already carry one. Chunks keep their ids: an id is a hash of the text, and the title
goes into the payload only.

ON by default (RAG_KB_TITLE_FILL_ENABLED=false turns it off). It runs at every startup; once a
collection is filled, a run is a single scroll over chunk metadata (never page text).
"""

import logging
import os
import threading
from typing import Any, Dict, List, Optional

logger = logging.getLogger(__name__)

SCROLL_BATCH = 512
RETRIEVE_BATCH = 64
WRITE_BATCH = 256
_TITLE_PREFIX = "Title:"
# Everything the fill needs from a chunk, and nothing else: no page text.
_PAYLOAD_FIELDS = [
    f"metadata.{key}"
    for key in ("title", "source_id", "page_id", "sys_id", "parent_id", "chunk_index", "line_number", "is_figure")
]


def is_enabled() -> bool:
    return os.environ.get("RAG_KB_TITLE_FILL_ENABLED", "true").strip().lower() == "true"


def title_from_text(text: Optional[str]) -> Optional[str]:
    """The title in a ServiceNow article's first line, or None when the text has none."""
    first = (text or "").split("\n", 1)[0].strip()
    if not first.startswith(_TITLE_PREFIX):
        return None
    return first[len(_TITLE_PREFIX) :].strip() or None


def _metadata(point) -> Dict[str, Any]:
    payload = point.payload if isinstance(point.payload, dict) else {}
    metadata = payload.get("metadata")
    return metadata if isinstance(metadata, dict) else {}


def _position(metadata: Dict[str, Any]) -> int:
    value = metadata.get("chunk_index")
    if value is None:
        value = metadata.get("line_number")
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def _scroll(client, collection_name: str):
    offset = None
    while True:
        batch, offset = client.scroll(
            collection_name=collection_name,
            limit=SCROLL_BATCH,
            offset=offset,
            with_payload=_PAYLOAD_FIELDS,
            with_vectors=False,
        )
        yield from batch
        if offset is None or not batch:
            return


def _pages(client, collection_name: str) -> List[List[Any]]:
    """The collection's chunks regrouped into the pages they came from, keyed like the
    backfill keys them: the scraper's own id, else the text hash, else the chunk itself."""
    from rag.core.documents.collection import _source_identifier

    groups: Dict[str, List[Any]] = {}
    for point in _scroll(client, collection_name):
        metadata = _metadata(point)
        key = _source_identifier(metadata) or metadata.get("parent_id") or str(point.id)
        groups.setdefault(str(key), []).append(point)
    return list(groups.values())


def _first_text_chunk(page: List[Any]):
    """The chunk that opens the page: the lowest position, a text chunk before a figure."""
    return min(page, key=lambda p: (bool(_metadata(p).get("is_figure")), _position(_metadata(p))))


def _titles_from_text(client, collection_name: str, point_ids: List[Any]) -> Dict[str, str]:
    """The title written in the first line of each of these chunks' text, by chunk id."""
    found: Dict[str, str] = {}
    for start in range(0, len(point_ids), RETRIEVE_BATCH):
        for point in client.retrieve(
            collection_name=collection_name,
            ids=point_ids[start : start + RETRIEVE_BATCH],
            with_payload=["page_content"],
            with_vectors=False,
        ):
            title = title_from_text((point.payload or {}).get("page_content"))
            if title:
                found[str(point.id)] = title
    return found


def _title(metadata: Dict[str, Any]) -> str:
    title = metadata.get("title")
    return title.strip() if isinstance(title, str) else ""


def _needs_update(page: List[Any]) -> bool:
    """Some chunk has no title, or the chunks disagree about it."""
    titles = {_title(_metadata(p)) for p in page}
    return "" in titles or len(titles) > 1


def _settled_title(page: List[Any], from_text: Dict[str, str]) -> Optional[str]:
    """The title the page's chunks should all carry, or None when nothing says."""
    first = _first_text_chunk(page)
    own = _title(_metadata(first))
    if own:
        return own
    if str(first.id) in from_text:
        return from_text[str(first.id)]
    titled = [p for p in page if _title(_metadata(p))]
    if not titled:
        return None
    return _title(_metadata(min(titled, key=lambda p: (bool(_metadata(p).get("is_figure")), _position(_metadata(p))))))


def fill_collection(client, collection_name: str) -> Dict[str, int]:
    """Give every chunk of every page in one collection the page's title. Returns counts."""
    result = {"pages_to_update": 0, "pages_updated": 0, "chunks_written": 0, "errors": 0}
    pages = [page for page in _pages(client, collection_name) if _needs_update(page)]
    result["pages_to_update"] = len(pages)
    if not pages:
        return result

    # Only a first chunk with no title of its own has to be read for the line in its text.
    unread = [_first_text_chunk(p).id for p in pages if not _title(_metadata(_first_text_chunk(p)))]
    from_text = _titles_from_text(client, collection_name, unread) if unread else {}

    for page in pages:
        title = _settled_title(page, from_text)
        if not title:
            continue
        targets = [p.id for p in page if _title(_metadata(p)) != title]
        try:
            for start in range(0, len(targets), WRITE_BATCH):
                client.set_payload(
                    collection_name=collection_name,
                    payload={"title": title},
                    points=targets[start : start + WRITE_BATCH],
                    key="metadata",
                    wait=True,
                )
        except Exception:
            # One page failing must not stop the rest; the next restart tries it again.
            result["errors"] += 1
            logger.exception("kb_title_fill: could not write a title in %s", collection_name)
            continue
        result["pages_updated"] += 1
        result["chunks_written"] += len(targets)
    return result


def run(client) -> Dict[str, int]:
    from rag.core.documents.kb_capability_backfill import _pending_knowledgebases

    summary = {"collections_scanned": 0, "pages_to_update": 0, "pages_updated": 0, "chunks_written": 0, "errors": 0}
    try:
        collections = _pending_knowledgebases()
    except Exception:
        logger.exception("kb_title_fill: could not list knowledgebases, aborting this run")
        summary["errors"] += 1
        return summary
    for kb in collections:
        if kb.is_manual:
            continue  # a manual KB is one document whose title is its name
        summary["collections_scanned"] += 1
        try:
            if not client.collection_exists(kb.name):
                continue
            result = fill_collection(client, kb.name)
        except Exception:
            summary["errors"] += 1
            logger.exception("kb_title_fill: %s failed", kb.name)
            continue
        for key, value in result.items():
            summary[key] += value
    logger.info("kb_title_fill: done - %s", summary)
    return summary


def start_background() -> threading.Thread:
    """Run on a daemon thread so startup is not held up by the scroll."""

    def _worker() -> None:
        try:
            from rag.qdrant.client import get_qdrant_client

            run(get_qdrant_client())
        except Exception:  # never take the process down from a daemon thread
            logger.exception("kb_title_fill: failed; it runs again on the next restart")

    thread = threading.Thread(target=_worker, daemon=True, name="kb-title-fill")
    thread.start()
    return thread
