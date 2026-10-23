"""Seed Postgres with the per-page labels that already sit on Qdrant chunks.

Postgres owns a page's classifier labels (llm_kb_capability_entries,
entry_type = 'kb_document'; docs/kb-label-ownership-spec.md). Pages labelled before
that table existed carry their labels only in the chunks' vector payload, so this
copies them across — once, with no LLM call, so nothing is charged and nothing is
re-classified.

One row per labelled page of every integration KB collection and one per manual KB
(``kb_<id>`` is a single document), keyed exactly as llm-server keys a label
(document_key.label_key). When a page's chunks disagree, the first text chunk in
document order speaks for it; figure chunks never do while a text chunk is labelled.
``source_version`` is the hash of the page's text as the loader computed it: the text
chunk's ``parent_id`` for a split page, or the chunk's own id for a page that fit in
one chunk (the id IS that hash). It is NULL where it cannot be recovered and for
manual KBs, whose rows are deleted on every data change instead of compared. ON
CONFLICT keeps an existing row as it is, apart from filling in empty capabilities; a label
written by a later classification is never overwritten.

Only KBs that are active and enabled, with a resolvable account, are listed
(kb_capability_backfill._pending_knowledgebases): a KB that is disabled when the seed
runs is not seeded, so enable it, or run the seed again, before this job is removed.

This is the ONE sanctioned read of labels from Qdrant (docs/kb-label-ownership-spec.md):
a one-time import, to be deleted once every environment has run it.

ON by default (RAG_KB_DOCUMENT_SEED_ENABLED=false turns it off). It runs at every
startup, not once: each run reads every labelled chunk's metadata (never page text),
inserts only the rows that are missing, and makes no LLM call. Running it each time is
deliberate: labels the classification backfill stamps on chunks after an earlier run are
imported on the next restart, which keeps the documents modal filled until a single
writer puts new labels into Postgres directly. It is deleted once that writer ships.
"""

import logging
import os
import threading
from typing import Any, Dict, List, Optional

from rag.core.documents.document_key import label_key

logger = logging.getLogger(__name__)

SCROLL_BATCH = 512
INSERT_BATCH = 500
_KINDS = ("sop", "knowledge")
# Everything the seed needs from a chunk, and nothing else: a legacy chunk's payload can
# carry the whole page's text, which a scroll with the full payload would drag along.
_PAYLOAD_FIELDS = [
    f"metadata.{key}"
    for key in (
        "kind",
        "shape",
        "capabilities",
        "source_id",
        "page_id",
        "sys_id",
        "url",
        "parent_id",
        "is_figure",
        "chunk_index",
        "line_number",
    )
]


def is_enabled() -> bool:
    return os.environ.get("RAG_KB_DOCUMENT_SEED_ENABLED", "true").strip().lower() == "true"


def _position(metadata: Dict[str, Any]) -> int:
    # An explicit None chunk_index must fall through to line_number, which dict.get's default would not.
    value = metadata.get("chunk_index")
    if value is None:
        value = metadata.get("line_number")
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def _capability_names(value: Any) -> List[str]:
    """The capability names a chunk carries; anything but a list of strings is not one (a bare
    string would otherwise be split into single characters)."""
    return [c for c in value if isinstance(c, str)] if isinstance(value, list) else []


def _labelled_points(client, collection_name: str):
    from qdrant_client import models

    scroll_filter = models.Filter(
        must=[models.FieldCondition(key="metadata.kind", match=models.MatchAny(any=list(_KINDS)))]
    )
    offset = None
    while True:
        batch, offset = client.scroll(
            collection_name=collection_name,
            scroll_filter=scroll_filter,
            limit=SCROLL_BATCH,
            offset=offset,
            with_payload=models.PayloadSelectorInclude(include=_PAYLOAD_FIELDS),
            with_vectors=False,
        )
        yield from batch
        if offset is None or not batch:
            return


def _version(point, metadata: Dict[str, Any]) -> Optional[str]:
    """The page-text hash a text chunk carries, or None when it is a figure chunk."""
    if metadata.get("is_figure"):
        return None
    parent = metadata.get("parent_id")
    if isinstance(parent, str) and parent:
        return parent
    return str(point.id).replace("-", "")


def page_rows(client, collection_name: str) -> List[Dict[str, Any]]:
    """One label row per keyable, labelled page of a collection (one row for a manual KB)."""
    manual = collection_name.startswith("kb_")
    best: Dict[str, tuple] = {}
    for point in _labelled_points(client, collection_name):
        payload = point.payload if isinstance(point.payload, dict) else {}
        metadata = payload.get("metadata")
        if not isinstance(metadata, dict):
            continue  # one malformed legacy point must not fail the whole collection
        url = metadata.get("url")
        key = label_key(collection_name, metadata, url if isinstance(url, str) else None)
        if not key:
            continue
        # A text chunk outranks a figure chunk; then the lowest position wins.
        rank = (bool(metadata.get("is_figure")), _position(metadata))
        if key not in best or rank < best[key][0]:
            best[key] = (rank, point, metadata)
    return [
        {
            "collection_name": collection_name,
            "document_key": key,
            "kind": metadata["kind"],
            "shape": metadata.get("shape"),
            "capabilities": _capability_names(metadata.get("capabilities")),
            "source_version": None if manual else _version(point, metadata),
        }
        for key, (_, point, metadata) in best.items()
    ]


def run(client) -> Dict[str, int]:
    from rag.core.documents.kb_capability_backfill import _pending_knowledgebases
    from rag.core.utils.db_query import seed_kb_document_labels

    summary = {"collections_scanned": 0, "pages_seen": 0, "rows_inserted": 0, "errors": 0}
    try:
        collections = _pending_knowledgebases()
    except Exception:
        logger.exception("kb_document_seed: could not list knowledgebases, aborting this run")
        summary["errors"] += 1
        return summary
    try:
        existing = {c.name for c in client.get_collections().collections}
    except Exception:
        logger.exception("kb_document_seed: could not list the Qdrant collections, aborting this run")
        summary["errors"] += 1
        return summary
    for kb in collections:
        summary["collections_scanned"] += 1
        try:
            if kb.name not in existing:
                continue
            rows = page_rows(client, kb.name)
            summary["pages_seen"] += len(rows)
            for start in range(0, len(rows), INSERT_BATCH):
                summary["rows_inserted"] += seed_kb_document_labels(rows[start : start + INSERT_BATCH])
        except Exception:
            summary["errors"] += 1
            logger.exception("kb_document_seed: %s failed", kb.name)
    logger.info("kb_document_seed: done - %s", summary)
    return summary


def start_background() -> threading.Thread:
    """Run on a daemon thread so startup is not held up by the scroll."""

    def _worker() -> None:
        try:
            from rag.qdrant.client import get_qdrant_client

            run(get_qdrant_client())
        except Exception:  # never take the process down from a daemon thread
            logger.exception("kb_document_seed: failed; it runs again on the next restart")

    thread = threading.Thread(target=_worker, daemon=True, name="kb-document-seed")
    thread.start()
    return thread
