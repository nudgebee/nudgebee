"""Startup backfill: classify existing knowledge bases that predate document
classification (added by PR #38623 / migration V977).

Classification (kind/shape/capabilities, stamped onto every chunk's vector
payload under ``metadata`` — what a metadata_filter matches at query time, and
the only record of whether a chunk is classified) runs inside the ingest path
(documents/processing.py), triggered by a manual upload or a scraper sync that
has an account to classify with. A KB ingested before that code shipped, and every
integration KB (its scrapers pass no account), has no labels at all — the
classification column in the app's Knowledge Base tab, and anything future
retrieval reads from these labels, silently has nothing for it, forever, unless
the document happens to be re-ingested. This migration closes that gap for every
account without requiring a re-ingest.

The unit is the source document, as at ingest (documents/processing.py
_classify_source_documents): a manual KB collection is ONE document (an upload
is one file however it was chunked), an integration KB collection is one
document per page — its chunks share a ``parent_id`` when the page was split,
and an unsplit page is its own single point. Each outstanding document costs
one LLM call and its labels are then written to EVERY chunk of it, in place,
with set_payload — exactly what a live ingest leaves behind.

A chunk is classified iff its payload carries a ``kind``, so nothing here
compares against a second store: a collection's steady state is one filtered
request asking Qdrant whether any point lacks a kind, and a document is
outstanding when any of its chunks does. Shared by every account of an
integration, so adding an account to one costs nothing.

After labelling, and for any collection that is fully labelled but has no
rollup row (the row the Knowledge Base tab reads, see
processing.refresh_kb_collection_summary — a swallowed write, or a table that
was recreated, leaves exactly that), the collection's rollup is rebuilt from
its payload. Rebuilding is not an LLM call, so it is not charged to the budget.

Shape of the run: collections are processed one at a time — deliberately not
parallelized across collections — so the whole run never exceeds
_classify_source_documents' own 5-worker cap on concurrent LLM calls, rather
than that cap multiplying by however many collections are outstanding.

Real LLM cost, not free compute like embedding_migration's on-device case: a
run classifies every account's backlog, and a multi-pod rolling deploy must
not multiply that spend by pod count. Holds the same kind of Postgres
advisory lock embedding_migration.py uses (own key, so the two migrations —
unrelated work — don't serialize behind each other) so only one replica
classifies at a time. Bounded per run via
RAG_KB_CAPABILITY_BACKFILL_MAX_DOCUMENTS_PER_RUN (LLM calls, not chunks) so a
large backlog drains over several restarts instead of one unattended burst
deciding the whole bill. The first account (sorted) of a collection supplies
the LLM.

Known limits, accepted rather than engineered around:
  * A collection is read whole when something in it is outstanding — a manual
    KB is one document. Chunks of PDF, JSON and CSV uploads carry no position,
    so their reconstructed order is Qdrant's rather than the upload's;
    classification samples across the whole text, so that does not change what
    it sees.
  * A document the model cannot classify (or whose provider is unreachable)
    stays outstanding and is attempted again on every run, within the budget.

Resumable and idempotent. Gated behind RAG_KB_CAPABILITY_BACKFILL_ENABLED,
default OFF — unlike the retag/embedding migrations this spends real money at
real production KB volume, so an operator opts in per environment rather than
inheriting it silently.
"""

import contextlib
import logging
import os
import threading
import time
from typing import Any, Callable, Dict, Iterator, List, NamedTuple, Optional, Set

logger = logging.getLogger(__name__)

# Own advisory-lock key, distinct from embedding_migration.LOCK_KEY — see the
# module docstring for why they must not share one.
LOCK_KEY = 8412772
_LOCK_CLASSID = LOCK_KEY >> 32
_LOCK_OBJID = LOCK_KEY & 0xFFFFFFFF

# Mirrors embedding_migration._STILL_HELD_SQL: a bare SELECT 1 only proves the
# connection answers, which a transparently reconnected session also does
# while holding no lock at all.
_STILL_HELD_SQL = """
SELECT EXISTS (
    SELECT 1 FROM pg_locks
    WHERE locktype = 'advisory'
      AND granted
      AND pid = pg_backend_pid()
      AND classid = :classid
      AND objid = :objid
      AND objsubid = 1
)
"""

SCROLL_BATCH = 256
# Points per set_payload request; a manual KB is one document, so its label
# write covers the whole collection and must not go out as one giant request.
PAYLOAD_BATCH = 500

# Label keys a chunk's payload ``metadata`` carries (processing.KB_LABEL_KEYS,
# kept in step by a test); stripped from the copies handed to the classifier so
# a stale label from an earlier run can never be mistaken for a fresh result.
_LABEL_KEYS = ("kind", "shape", "capabilities")
# The kinds that make a chunk count as classified (processing._KB_KINDS).
_KINDS = ["sop", "knowledge"]

# Total LLM calls (documents) attempted in one run, across every account. Bounds
# one run's spend and wall-clock so a large backlog drains over several
# restarts rather than one unattended burst.
DEFAULT_MAX_DOCUMENTS_PER_RUN = 500

# Background retry, same policy as embedding_migration: a transient failure
# (provider outage, Postgres blip) during startup is retried rather than
# logged and forgotten, since the next restart may be a long time away.
RETRY_ATTEMPTS = 5
RETRY_DELAY_SECONDS = 60
RETRY_MAX_DELAY_SECONDS = 900


class MigrationUnavailable(RuntimeError):
    """The lock could not be evaluated at all — Postgres was unreachable."""


class KBCollection(NamedTuple):
    name: str
    is_manual: bool
    account_ids: List[str]


class CollectionResult(NamedTuple):
    attempted: int = 0
    labelled_documents: int = 0
    labelled_chunks: int = 0
    errors: int = 0
    summary_rebuilt: int = 0


@contextlib.contextmanager
def _exclusive_run() -> Iterator[Optional[Callable[[], bool]]]:
    """Yield a liveness check to exactly one replica at a time, else None.

    Same shape as embedding_migration._exclusive_run (own lock key) — see
    there for the full reasoning on AUTOCOMMIT and the still-held re-check.
    """
    from sqlalchemy import text

    from rag.core.utils.db_query import engine

    conn = None
    acquired = False
    try:
        conn = engine.connect().execution_options(isolation_level="AUTOCOMMIT")
        acquired = bool(conn.execute(text("SELECT pg_try_advisory_lock(:k)"), {"k": LOCK_KEY}).scalar())
        if not acquired:
            logger.info("kb_capability_backfill: another replica holds the migration lock, skipping this run")
    except Exception as exc:
        with contextlib.suppress(Exception):
            if conn is not None:
                conn.close()
        raise MigrationUnavailable(f"could not evaluate the migration lock: {exc}") from exc

    def still_held() -> bool:
        try:
            held = bool(conn.execute(text(_STILL_HELD_SQL), {"classid": _LOCK_CLASSID, "objid": _LOCK_OBJID}).scalar())
            if not held:
                logger.warning("kb_capability_backfill: the advisory lock is no longer held by this session")
            return held
        except Exception as exc:
            logger.warning("kb_capability_backfill: could not confirm the migration lock (%s)", exc)
            return False

    try:
        yield still_held if acquired else None
    finally:
        if conn is not None:
            if acquired:
                with contextlib.suppress(Exception):
                    conn.execute(text("SELECT pg_advisory_unlock(:k)"), {"k": LOCK_KEY})
            with contextlib.suppress(Exception):
                conn.close()


def is_enabled() -> bool:
    """Opt-in, default off. Unlike module_retag/embedding_migration this makes
    real LLM calls at real production KB volume — an operator greenlights it
    per environment rather than inheriting it from a code default."""
    return os.environ.get("RAG_KB_CAPABILITY_BACKFILL_ENABLED", "false").strip().lower() == "true"


def _max_documents_per_run() -> int:
    try:
        return int(os.environ.get("RAG_KB_CAPABILITY_BACKFILL_MAX_DOCUMENTS_PER_RUN", DEFAULT_MAX_DOCUMENTS_PER_RUN))
    except ValueError:
        return DEFAULT_MAX_DOCUMENTS_PER_RUN


def _pending_knowledgebases() -> List[KBCollection]:
    """Every active, enabled KB's collection, deduped by collection name, with
    every account that maps to it — integration KBs share one collection per
    integration, one KB row per cloud account.

    Mirrors kbCollectionName in llm-server's tools/core/knowledgebase_service.go;
    the two must stay in sync or this looks at the wrong collection.
    """
    from sqlalchemy import text

    from rag.core.utils.db_query import engine

    seen: Dict[str, Dict[str, Any]] = {}
    with engine.connect() as connection:
        rows = connection.execute(
            text(
                "SELECT id, account_id, kb_type, integration_id FROM llm_knowledgebases "
                "WHERE status = 'active' AND enabled = true"
            )
        )
        for row in rows:
            if row.kb_type == "integration" and row.integration_id:
                name, is_manual = f"{row.integration_id}_knowledge_base", False
            else:
                name, is_manual = f"kb_{row.id}", True
            entry = seen.setdefault(name, {"is_manual": is_manual, "accounts": set()})
            entry["accounts"].add(str(row.account_id))
    return [KBCollection(name, e["is_manual"], sorted(e["accounts"])) for name, e in seen.items()]


def _chunk_id(point_id: Any) -> str:
    """The bare 32-char hex id ingest hashed from the content; Qdrant stores and
    returns it in canonical hyphenated UUID form (see
    processing._normalize_point_id), so a raw string compare never matches."""
    return str(point_id).replace("-", "")


def _kind_condition():
    from qdrant_client import models

    return models.FieldCondition(key="metadata.kind", match=models.MatchAny(any=_KINDS))


def _any_point(client, collection_name: str, *, labelled: bool) -> bool:
    """Whether the collection holds a point that carries a classification
    (`labelled`) or lacks one, decided by Qdrant in one filtered request — no
    payload is read."""
    from qdrant_client import models

    condition = _kind_condition()
    scroll_filter = models.Filter(must=[condition]) if labelled else models.Filter(must_not=[condition])
    points, _ = client.scroll(
        collection_name=collection_name,
        scroll_filter=scroll_filter,
        limit=1,
        with_payload=False,
        with_vectors=False,
    )
    return bool(points)


def _scroll_all(client, collection_name: str) -> List[Any]:
    points: List[Any] = []
    offset = None
    while True:
        batch, offset = client.scroll(
            collection_name=collection_name,
            limit=SCROLL_BATCH,
            offset=offset,
            with_payload=True,
            with_vectors=False,
        )
        points.extend(batch)
        if offset is None or not batch:
            return points


def _metadata(point) -> Dict[str, Any]:
    return (point.payload or {}).get("metadata") or {}


def _is_labelled(point) -> bool:
    return _metadata(point).get("kind") in _KINDS


def _position(point) -> int:
    md = _metadata(point)
    try:
        return int(md.get("chunk_index", md.get("line_number", 0)) or 0)
    except (TypeError, ValueError):
        return 0


def _group_points(kb: KBCollection, points: List[Any]) -> List[List[Any]]:
    """Regroup a collection's chunks into the source documents they came from.

    A manual KB collection is one document. An integration collection is one
    document per page, identified by the id its scraper stamped (Confluence
    page_id, ServiceNow sys_id — the same id the source store and the Fact/SOP
    marks key a document on), which every chunk of the page carries, including
    its figure descriptions. That id, not ``parent_id``, is the key: a page's
    ``parent_id`` is a hash of its whole text, so after an edit the chunks that
    did not change keep the old one while the new chunks carry a new one, and
    grouping on it would classify the edited part of a page on its own. Points
    that carry no scraper id fall back to ``parent_id`` (chunks of a split page)
    and finally to their own id (a page that fit in one chunk).
    """
    from rag.core.documents.collection import _source_identifier

    if not points:
        return []
    if kb.is_manual:
        return [sorted(points, key=_position)]
    groups: Dict[str, List[Any]] = {}
    for point in points:
        metadata = _metadata(point)
        key = _source_identifier(metadata) or metadata.get("parent_id") or _chunk_id(point.id)
        groups.setdefault(str(key), []).append(point)
    return [sorted(group, key=_position) for group in groups.values()]


def _to_documents(group: List[Any]) -> List[Any]:
    from rag.core.types import Document

    return [
        Document(
            page_content=(point.payload or {}).get("page_content") or "",
            metadata={k: v for k, v in _metadata(point).items() if k not in _LABEL_KEYS},
            id=_chunk_id(point.id),
        )
        for point in group
    ]


def _write_labels(client, kb: KBCollection, group: List[Any], labels: Dict[str, Any]) -> None:
    """Land a document's labels on every one of its chunks, in place."""
    point_ids = [point.id for point in group]
    for start in range(0, len(point_ids), PAYLOAD_BATCH):
        client.set_payload(
            collection_name=kb.name,
            payload=labels,
            points=point_ids[start : start + PAYLOAD_BATCH],
            key="metadata",
            wait=True,
        )


def _backfill_collection(
    client, kb: KBCollection, budget: int, summarised: Optional[Set[str]] = None
) -> CollectionResult:
    """Classify and label at most `budget` outstanding documents of one
    collection, then bring its rollup row in line with what its payload says.

    `summarised` is the set of collections that already have a rollup row, or
    None when it could not be read (then no rollup is rebuilt from it)."""
    if not client.collection_exists(kb.name):
        # An integration KB can be "active" with nothing loaded: its scraper
        # flips the status even when it found no documents, so no collection was
        # ever created. That is nothing to backfill, not a failure to retry.
        return CollectionResult()

    from rag.core.documents.processing import refresh_kb_collection_summary

    # The steady state (everything already labelled) must not cost a payload
    # read of every collection on every restart: Qdrant answers this one.
    if budget <= 0 or not _any_point(client, kb.name, labelled=False):
        if summarised is not None and kb.name not in summarised and _any_point(client, kb.name, labelled=True):
            refresh_kb_collection_summary(kb.name)
            return CollectionResult(summary_rebuilt=1)
        return CollectionResult()

    points = _scroll_all(client, kb.name)
    outstanding = [group for group in _group_points(kb, points) if any(not _is_labelled(point) for point in group)][
        :budget
    ]
    if not outstanding:
        return CollectionResult()

    from rag.core.documents.processing import _classify_source_documents

    logger.info(
        "kb_capability_backfill: classifying %d outstanding document(s) in %s (accounts %s)",
        len(outstanding),
        kb.name,
        kb.account_ids,
    )
    classified = [_to_documents(group) for group in outstanding]
    # Only calls that were really made spend budget: an account with no
    # resolvable LLM provider costs nothing, and must not starve the
    # collections after it.
    attempted = _classify_source_documents(classified, kb.account_ids[0], kb.name)

    labelled_documents = labelled_chunks = errors = 0
    for group, documents in zip(outstanding, classified):
        stamped = documents[0].metadata
        if "kind" not in stamped:
            continue  # classification failed; stays outstanding for the next run
        labels = {key: stamped[key] for key in _LABEL_KEYS if key in stamped}
        try:
            _write_labels(client, kb, group, labels)
        except Exception:
            errors += 1
            logger.exception("kb_capability_backfill: could not write labels for a document in %s", kb.name)
            continue
        labelled_documents += 1
        labelled_chunks += len(group)
    rebuilt = 0
    if labelled_documents:
        refresh_kb_collection_summary(kb.name)
    elif summarised is not None and kb.name not in summarised and any(_is_labelled(point) for point in points):
        # Nothing new was labelled — say the one document that will not classify — but
        # chunks already are, and no row says so: the reconcile must not depend on
        # every document in the collection classifying.
        refresh_kb_collection_summary(kb.name)
        rebuilt = 1
    return CollectionResult(attempted, labelled_documents, labelled_chunks, errors, rebuilt)


def run(client, still_held: Callable[[], bool] = lambda: True) -> Dict[str, int]:
    """Classify outstanding documents across every active, enabled KB, bounded
    by RAG_KB_CAPABILITY_BACKFILL_MAX_DOCUMENTS_PER_RUN, and rebuild any missing
    rollup. Safe to call repeatedly — a fully labelled collection with a rollup
    costs one Postgres read per run and one filtered request."""
    summary = {
        "collections_scanned": 0,
        "collections_backfilled": 0,
        "documents_labelled": 0,
        "chunks_labelled": 0,
        "summaries_rebuilt": 0,
        "errors": 0,
    }
    budget = _max_documents_per_run()
    try:
        pending = _pending_knowledgebases()
    except Exception:
        logger.exception("kb_capability_backfill: could not list knowledgebases, aborting this run")
        # Counted, so start_background retries: a Postgres blip at startup must not
        # look like a run that finished with nothing to do.
        summary["errors"] += 1
        return summary
    try:
        from rag.core.utils.db_query import get_kb_summary_collection_names

        summarised: Optional[Set[str]] = get_kb_summary_collection_names()
    except Exception:
        logger.exception("kb_capability_backfill: could not list the existing rollups, rebuilding none this run")
        summarised = None

    summary["collections_scanned"] = len(pending)
    for kb in pending:
        if not still_held():
            logger.warning("kb_capability_backfill: stopping early, the migration lock is no longer held")
            summary["stopped_lock_lost"] = 1
            break
        if budget <= 0 and "budget_exhausted" not in summary:
            logger.info(
                "kb_capability_backfill: per-run budget exhausted, only rebuilding rollups; resumes next restart"
            )
            summary["budget_exhausted"] = 1
        try:
            result = _backfill_collection(client, kb, budget, summarised)
        except Exception:
            summary["errors"] += 1
            logger.exception("kb_capability_backfill: %s failed", kb.name)
            continue
        budget -= result.attempted
        summary["errors"] += result.errors
        summary["summaries_rebuilt"] += result.summary_rebuilt
        if result.labelled_documents:
            summary["collections_backfilled"] += 1
            summary["documents_labelled"] += result.labelled_documents
            summary["chunks_labelled"] += result.labelled_chunks

    logger.info("kb_capability_backfill: done - %s", summary)
    return summary


def run_locked() -> Dict[str, int]:
    """Entry point: acquire the cross-replica lock, then run()."""
    from rag.qdrant.client import get_qdrant_client

    with _exclusive_run() as still_held:
        if still_held is None:
            return {"skipped_locked": 1}
        return run(get_qdrant_client(), still_held)


def start_background(retry_delay: int = RETRY_DELAY_SECONDS) -> threading.Thread:
    """Run on a daemon thread so startup isn't held up by classification
    calls across every account's backlog."""

    def _worker() -> None:
        delay = retry_delay
        for attempt in range(1, RETRY_ATTEMPTS + 1):
            try:
                summary = run_locked()
                if summary.get("errors"):
                    # Every step is idempotent, so a retry only redoes what's
                    # still outstanding.
                    raise RuntimeError(f"{summary['errors']} collection(s) failed to backfill")
                return
            except Exception as exc:  # never take the process down from a daemon thread
                if attempt == RETRY_ATTEMPTS:
                    logger.exception(
                        "kb_capability_backfill: giving up after %d attempts (%s); remaining documents stay "
                        "unclassified until the next restart",
                        attempt,
                        exc,
                    )
                    return
                logger.warning("kb_capability_backfill: attempt %d failed (%s); retrying in %ds", attempt, exc, delay)
                time.sleep(delay)
                delay = min(delay * 2, RETRY_MAX_DELAY_SECONDS)

    thread = threading.Thread(target=_worker, daemon=True, name="kb-capability-backfill")
    thread.start()
    return thread
