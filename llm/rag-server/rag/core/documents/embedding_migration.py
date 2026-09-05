"""Rebuild Qdrant collections whose vectors no longer come from the configured
embedding model.

Vectors are only comparable to other vectors from the same model. Change
``EMBEDDINGS_PROVIDER``/``EMBEDDINGS_MODEL_NAME`` and every stored vector becomes
unusable: queries land in the new space, the points are in the old one, and
searches return nothing. A collection's dimension is fixed at creation, so the
data has to be rebuilt rather than updated.

Why the model NAME is recorded and not just the dimension: two different models
routinely share one. ``bge-small-en-v1.5`` and ``all-MiniLM-L6-v2`` are both 384,
so a dimension check would call a swap between them "already correct" and leave
every vector silently wrong. Collections are stamped with the model that built
them (in the same Qdrant collection metadata that already carries ``module`` and
``embeddings_generated_at``) and the stamp is what the decision reads.

Shape of the run: startup only *registers* what needs rebuilding, which is a
handful of metadata reads, and the rebuilding happens on a background thread
afterwards. A full re-embed takes hours; doing it inline would hold the service
out of readiness for that whole time.

Safety, which drives the rest: document text exists *only* in the Qdrant payload,
so a delete-then-rebuild that dies halfway destroys data with no source to
recover from. Each collection is rebuilt into a staging collection first, and the
original is dropped only once staging holds every point. Re-embedding happens
once; the final swap is a vector copy.

Resumable and idempotent: a collection already stamped with the target model is
skipped, and a complete staging collection from an interrupted run is reused
rather than re-embedded, so a crash costs the collection in flight at worst.

Gated behind ``RAG_EMBEDDING_MIGRATION_ENABLED``, default OFF: this rewrites
every collection and is not something to switch on implicitly.
"""

import contextlib
import logging
import os
import threading
import time
import uuid
from typing import Any, Callable, Dict, Iterator, List, NamedTuple, Optional, Tuple

from qdrant_client import QdrantClient
from qdrant_client.models import Distance, PointStruct, VectorParams

logger = logging.getLogger(__name__)


class MigrationUnavailable(RuntimeError):
    """The lock could not be evaluated at all -- Postgres was unreachable.

    Distinct from "another replica holds it", which is a healthy outcome that
    needs no retry. Collapsing the two would let a database blip look like a
    successful skip and leave the rebuild permanently undone.
    """


# Collection-metadata key holding the model that produced the vectors.
MODEL_KEY = "embedding_model"

# Small batches keep each upsert inside the default httpx read timeout; the
# retag migration hit intermittent ReadTimeouts at 128 points per upsert.
SCROLL_BATCH = 64
EMBED_BATCH = 32
STAGING_SUFFIX = "__embmig"

# Background retry. A rebuild that never runs is not a no-op: with the provider
# already switched, queries land in the new space while the stored vectors are
# still in the old one, and every search returns nothing. So a transient
# failure at startup has to be retried rather than logged and forgotten.
RETRY_ATTEMPTS = 5
RETRY_DELAY_SECONDS = 60
RETRY_MAX_DELAY_SECONDS = 900

# Postgres advisory lock id. Arbitrary but fixed: every replica must ask for the
# same one. Advisory locks are session-scoped, so a pod that dies mid-rebuild
# releases it automatically rather than wedging every future deploy.
LOCK_KEY = 8412771
# pg_locks splits a bigint advisory key across (classid, objid); objsubid is 1
# for the single-argument form of pg_try_advisory_lock.
_LOCK_CLASSID = LOCK_KEY >> 32
_LOCK_OBJID = LOCK_KEY & 0xFFFFFFFF

# Asking Postgres whether *this* backend still holds the lock. A bare SELECT 1
# only proves the connection answers: SQLAlchemy can hand back a transparently
# reconnected session, which succeeds while holding no lock at all, and two
# replicas would then rebuild the same collection.
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


@contextlib.contextmanager
def _exclusive_run() -> Iterator[Optional[Callable[[], bool]]]:
    """Yield a liveness check to exactly one replica at a time, else None.

    Every pod runs this at startup, and they share one Qdrant. Two pods
    rebuilding the same collection would have one delete the original while the
    other is still reading it -- data loss, not just a failed migration. The
    process-local FileLock used elsewhere in startup cannot help: it guards
    workers inside one pod, not pods.

    Fails closed. If the lock cannot be taken -- another pod holds it, or the
    database is unreachable -- nothing is rebuilt. Skipping is recoverable on
    the next start; two pods rewriting the same collection is not.

    The yielded callable asks Postgres whether this backend still holds the
    lock. Advisory locks die with their session, and a rebuild runs for hours
    doing no Postgres work, so a proxy or ``idle_in_transaction_session_timeout``
    can drop the connection mid-run. Calling it keeps the connection from going
    idle and, more importantly, catches the case where the pool handed back a
    reconnected session -- that answers queries perfectly well while holding no
    lock, which a liveness ping alone would read as success.
    """
    from sqlalchemy import text

    from rag.core.utils.db_query import engine

    conn = None
    acquired = False
    # Acquisition is resolved before the yield. Handling it around the yield
    # would catch exceptions thrown back in from the caller's body and try to
    # yield a second time, which raises "generator didn't stop after throw()"
    # and hides the real failure.
    try:
        # AUTOCOMMIT so the connection never sits "idle in transaction" for the
        # hours a rebuild takes. SQLAlchemy otherwise opens a transaction on the
        # first statement and leaves it open, which is the state
        # idle_in_transaction_session_timeout and most proxies kill first.
        # Advisory locks are session-scoped, so they outlive the statement.
        conn = engine.connect().execution_options(isolation_level="AUTOCOMMIT")
        acquired = bool(conn.execute(text("SELECT pg_try_advisory_lock(:k)"), {"k": LOCK_KEY}).scalar())
        if not acquired:
            logger.info("embedding_migration: another replica holds the migration lock, skipping this run")
    except Exception as exc:
        # Deliberately not swallowed: the caller has to be able to tell a
        # database failure from a healthy skip so it can retry.
        with contextlib.suppress(Exception):
            if conn is not None:
                conn.close()
        raise MigrationUnavailable(f"could not evaluate the migration lock: {exc}") from exc

    def still_held() -> bool:
        try:
            held = bool(conn.execute(text(_STILL_HELD_SQL), {"classid": _LOCK_CLASSID, "objid": _LOCK_OBJID}).scalar())
            if not held:
                logger.warning("embedding_migration: the advisory lock is no longer held by this session")
            return held
        except Exception as exc:
            logger.warning("embedding_migration: could not confirm the migration lock (%s)", exc)
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


class Pending(NamedTuple):
    """One collection registered for rebuilding."""

    name: str
    account: str
    stored_model: str
    target_model: str
    target_dim: int
    distance: Distance
    # The collection's existing metadata, carried through the rebuild. It holds
    # module/account/tenant_id, which decide which searches reach this
    # collection at all -- recreating without them makes the data invisible.
    metadata: Dict[str, Any]


def is_enabled() -> bool:
    """Opt-in only. Defaults OFF because this rewrites every collection."""
    return os.environ.get("RAG_EMBEDDING_MIGRATION_ENABLED", "false").lower() == "true"


def model_identity(embeddings: Any) -> str:
    """Stable name for the model behind an Embeddings instance.

    Providers set ``model_id``; the on-device backend reads its name from
    config. Either way this is what gets stamped and compared.
    """
    from utils.config import Config

    return str(getattr(embeddings, "model_id", None) or Config.embeddings_model_id or "unknown")


def _account_of(collection: str) -> str:
    """Account id a collection belongs to, or "" for global ones.

    Embedding config is per-account: an account with its own embeddings
    integration must be re-embedded with *its* model, not the deployment
    default, or its collection lands in a space its own queries never reach.
    Collections are named ``<account_uuid>_<module>``; anything else
    (``nudgebee_docs``, ``kubectl``, ``kb_<id>``) is global.
    """
    head = collection.split("_", 1)[0]
    try:
        uuid.UUID(head)
    except (ValueError, AttributeError):
        return ""
    return head


def _text_of(payload: Optional[Dict[str, Any]]) -> str:
    """The field the ingest pipeline writes document text into.

    Defensive about shape: this reads payloads written by past versions of the
    ingest, and a surprise here would abort a rebuild rather than skip a point.
    """
    if not isinstance(payload, dict):
        return ""
    value = payload.get("page_content")
    return "" if value is None else str(value)


def _describe(client: QdrantClient, name: str) -> Optional[Tuple[int, Distance, Dict[str, Any], int]]:
    """(dim, distance, metadata, points) for a plain single-vector collection.

    Returns None for named-vector collections: none exist today, and guessing at
    their layout during a destructive rebuild is not worth the risk.
    """
    info = client.get_collection(name)
    params = info.config.params.vectors
    if not isinstance(params, VectorParams):
        logger.warning("embedding_migration: %s uses named vectors, skipping", name)
        return None
    metadata = getattr(info.config, "metadata", None) or {}
    return params.size, params.distance, metadata, info.points_count or 0


def _survey(client: QdrantClient) -> Tuple[List[Any], set, set]:
    """Collections, the active names, and the sources with leftover staging."""
    cols = list(client.get_collections().collections)
    active = {c.name for c in cols if not c.name.endswith(STAGING_SUFFIX)}
    interrupted = {c.name[: -len(STAGING_SUFFIX)] for c in cols if c.name.endswith(STAGING_SUFFIX)}
    return cols, active, interrupted


def _target_for(
    account: str, embeddings_for: Callable[[str], Any], resolved: Dict[str, Optional[Tuple[str, int]]]
) -> Optional[Tuple[str, int]]:
    """(model name, dimension) for an account, or None if it cannot be resolved.

    The failure is cached: without it a provider outage is retried once per
    collection, which for an account with many collections is a long series of
    blocking calls that all fail the same way.
    """
    if account not in resolved:
        try:
            model = embeddings_for(account)
            resolved[account] = (model_identity(model), len(model.embed_query("dimension probe")))
        except Exception as exc:
            logger.warning(
                "embedding_migration: could not resolve the embedding model for account %r (%s); "
                "its collections are left alone this run",
                account or "<global>",
                exc,
            )
            resolved[account] = None
    return resolved[account]


def _orphan_pending(client: QdrantClient, src: str) -> Optional[Pending]:
    """Work item for a staging collection whose original no longer exists.

    The run died between dropping the original and recreating it, so staging
    holds the only copy of that data. Its own config describes what to restore.
    """
    described = _describe(client, f"{src}{STAGING_SUFFIX}")
    if described is None:
        return None
    dim, distance, metadata, _points = described
    return Pending(src, _account_of(src), "interrupted", str(metadata.get(MODEL_KEY) or ""), dim, distance, metadata)


def register(client: QdrantClient, embeddings_for: Callable[[str], Any]) -> List[Pending]:
    """Decide what needs rebuilding. Reads metadata only — no embedding work."""
    pending: List[Pending] = []
    resolved: Dict[str, Optional[Tuple[str, int]]] = {}
    unstamped = 0

    cols, active, interrupted = _survey(client)
    # Staging left behind means a rebuild did not finish. Its source is rebuilt
    # again whatever its stamp says, because the crash may have come after the
    # new collection was created and stamped but before every point reached it.
    orphaned = sorted(interrupted - active)
    if interrupted:
        logger.info("embedding_migration: %d collection(s) have staging from an interrupted run", len(interrupted))
    if orphaned:
        # Iterating active collections would never see these, and staging is
        # the only remaining copy.
        logger.warning(
            "embedding_migration: %d collection(s) exist only as staging and will be restored: %s",
            len(orphaned),
            ", ".join(orphaned[:10]),
        )

    for c in cols:
        name = c.name
        if name.endswith(STAGING_SUFFIX):
            continue
        try:
            described = _describe(client, name)
            if described is None:
                continue
            dim, distance, metadata, _points = described
            account = _account_of(name)
            target = _target_for(account, embeddings_for, resolved)
            if target is None:
                continue
            target_model, target_dim = target
            stored_model = str(metadata.get(MODEL_KEY) or "")

            if name in interrupted:
                pass  # resume, whatever the stamp claims
            elif stored_model:
                if stored_model == target_model:
                    continue
            elif dim == target_dim:
                # Predates the stamp. Same dimension is the best evidence
                # available that it came from this model, and rebuilding every
                # unstamped collection on a hunch costs hours. Two models of
                # equal dimension are indistinguishable here; that exposure
                # ends once a collection has been stamped.
                unstamped += 1
                continue
            pending.append(
                Pending(name, account, stored_model or "unstamped", target_model, target_dim, distance, metadata)
            )
        except Exception as exc:
            logger.exception("embedding_migration: could not inspect %s (%s)", name, exc)

    for src in orphaned:
        try:
            orphan = _orphan_pending(client, src)
            if orphan is not None:
                pending.append(orphan)
        except Exception as exc:
            logger.exception("embedding_migration: could not inspect staging for %s (%s)", src, exc)

    if unstamped:
        logger.info(
            "embedding_migration: %d collection(s) predate the model stamp but match the "
            "expected dimension; treating them as current",
            unstamped,
        )
    return pending


def _still_mine(still_held: Callable[[], bool], what: str) -> None:
    """Abort before touching anything if the cross-replica lock is gone.

    Checking only between collections is not enough: one collection can take
    hours on its own, and losing the lock part-way means another replica may
    already be rebuilding it. Raising leaves staging in place, so the next run
    resumes rather than re-embedding.
    """
    if not still_held():
        raise RuntimeError(f"migration lock lost during {what}; stopping before touching more data")


def _rebuild_into(
    client: QdrantClient,
    source: str,
    target: str,
    embeddings: Any,
    p: Pending,
    still_held: Callable[[], bool] = lambda: True,
) -> int:
    """Re-embed every point of ``source`` into a fresh ``target``. Returns count."""
    # Clear any partial staging from an interrupted run. Qdrant 404s on a
    # collection that was never created, which is the normal first-run case.
    try:
        client.delete_collection(target)
    except Exception:
        pass
    client.create_collection(
        target,
        vectors_config=VectorParams(size=p.target_dim, distance=p.distance),
        metadata={**p.metadata, MODEL_KEY: p.target_model},
    )
    moved, offset = 0, None
    while True:
        _still_mine(still_held, f"re-embedding {source}")
        points, offset = client.scroll(
            collection_name=source, limit=SCROLL_BATCH, offset=offset, with_payload=True, with_vectors=False
        )
        if not points:
            break
        # Points whose payload lost its text cannot be re-embedded. Dropping
        # them silently would shrink the corpus invisibly, so they are counted
        # and reported rather than skipped quietly.
        usable = [pt for pt in points if _text_of(pt.payload).strip()]
        if len(usable) != len(points):
            logger.warning(
                "embedding_migration: %s has %d point(s) with no page_content; they cannot be "
                "re-embedded and will not survive the rebuild",
                source,
                len(points) - len(usable),
            )
        for i in range(0, len(usable), EMBED_BATCH):
            chunk = usable[i : i + EMBED_BATCH]
            vectors = embeddings.embed_documents([_text_of(pt.payload) for pt in chunk])
            if len(vectors) != len(chunk):
                # zip() would silently truncate or pair payloads with the wrong
                # vectors, and the original collection is deleted once staging
                # looks complete -- so a short batch has to stop this
                # collection, not quietly shrink it.
                raise RuntimeError(f"embedding returned {len(vectors)} vectors for {len(chunk)} documents in {source}")
            client.upsert(
                collection_name=target,
                points=[PointStruct(id=pt.id, vector=v, payload=pt.payload) for pt, v in zip(chunk, vectors)],
                wait=True,
            )
            moved += len(chunk)
        if offset is None:
            break
    return moved


def _copy_vectors(client: QdrantClient, source: str, target: str, still_held: Callable[[], bool] = lambda: True) -> int:
    """Copy points verbatim — vectors already computed, so no embedding cost."""
    copied, offset = 0, None
    while True:
        _still_mine(still_held, f"restoring {target}")
        points, offset = client.scroll(
            collection_name=source, limit=SCROLL_BATCH, offset=offset, with_payload=True, with_vectors=True
        )
        if not points:
            break
        client.upsert(
            collection_name=target,
            points=[PointStruct(id=pt.id, vector=pt.vector, payload=pt.payload) for pt in points],
            wait=True,
        )
        copied += len(points)
        if offset is None:
            break
    return copied


def rebuild(
    client: QdrantClient,
    p: Pending,
    load_embeddings: Callable[[], Any],
    still_held: Callable[[], bool] = lambda: True,
) -> str:
    """Rebuild one registered collection. Returns a status word.

    ``load_embeddings`` is a factory, not a model, and is only called when
    something actually has to be re-embedded. Restoring an orphan or resuming
    from complete staging needs no embeddings at all -- and those are exactly
    the recovery paths that should still work while the provider is down.
    """
    staging = f"{p.name}{STAGING_SUFFIX}"
    try:
        expected = client.get_collection(p.name).points_count or 0
    except Exception:
        # Restoring an orphan: the original was already dropped, so staging is
        # the only copy and there is nothing to compare against.
        expected = 0

    reuse = False
    try:
        staged = client.get_collection(staging).points_count or 0
        reuse = expected == 0 or staged >= expected
    except Exception:
        reuse = False

    if reuse:
        logger.info("embedding_migration: reusing staged rebuild for %s", p.name)
    else:
        moved = _rebuild_into(client, p.name, staging, load_embeddings(), p, still_held)
        logger.info("embedding_migration: staged %d/%d points for %s", moved, expected, p.name)

    # The last moment before anything is destroyed. Everything above this line
    # only added data; everything below removes the original.
    _still_mine(still_held, f"the swap for {p.name}")

    # Only now is the original expendable: staging holds the data. It may
    # already be gone when restoring an orphan.
    with contextlib.suppress(Exception):
        client.delete_collection(p.name)
    client.create_collection(
        p.name,
        vectors_config=VectorParams(size=p.target_dim, distance=p.distance),
        metadata={**p.metadata, MODEL_KEY: p.target_model},
    )
    copied = _copy_vectors(client, staging, p.name, still_held)
    try:
        client.delete_collection(staging)
    except Exception as exc:
        # The collection is already rebuilt and correct at this point. Letting a
        # failed cleanup mark the whole thing failed would retry work that is
        # done; the leftover staging is picked up as interrupted next start,
        # which is wasteful but harmless.
        logger.warning(
            "embedding_migration: %s rebuilt, but its staging collection could not be removed (%s)",
            p.name,
            exc,
        )
    logger.info(
        "embedding_migration: %s rebuilt with %s (%d points, was %s)",
        p.name,
        p.target_model,
        copied,
        p.stored_model,
    )
    return "rebuilt"


def run(client: QdrantClient, embeddings_for: Callable[[str], Any]) -> Dict[str, int]:
    """Register, then rebuild everything registered. Safe to call repeatedly.

    Holds a cross-replica lock for the whole run; see ``_exclusive_run``.
    """
    with _exclusive_run() as still_held:
        if still_held is None:
            return {"registered": 0, "rebuilt": 0, "failed": 0, "skipped_locked": 1}
        return _run_locked(client, embeddings_for, still_held)


def _run_locked(
    client: QdrantClient, embeddings_for: Callable[[str], Any], still_held: Callable[[], bool]
) -> Dict[str, int]:
    started = time.monotonic()
    pending = register(client, embeddings_for)
    summary = {"registered": len(pending), "rebuilt": 0, "failed": 0}
    if not pending:
        logger.info("embedding_migration: nothing to rebuild")
        return summary
    logger.info(
        "embedding_migration: %d collection(s) registered for rebuild: %s",
        len(pending),
        ", ".join(p.name for p in pending[:10]) + ("..." if len(pending) > 10 else ""),
    )
    for p in pending:
        if not still_held():
            # Carrying on without the lock risks a second replica rebuilding the
            # same collection. Stopping leaves staging intact, so the next start
            # resumes rather than re-embedding.
            logger.warning("embedding_migration: stopping early, the migration lock is no longer held")
            summary["stopped_lock_lost"] = 1
            break
        try:
            rebuild(client, p, lambda: embeddings_for(p.account), still_held)
            summary["rebuilt"] += 1
        except Exception as exc:
            if not still_held():
                # Not this collection's fault: the lock went away mid-rebuild
                # and rebuild() stopped on purpose. Counting it as a failure
                # and printing a traceback would misreport a deliberate stop,
                # and breaking only on the next iteration misses the case where
                # this was the last collection.
                logger.warning("embedding_migration: stopping early, the migration lock is no longer held")
                summary["stopped_lock_lost"] = 1
                break
            # One bad collection must not strand the rest; the staging copy is
            # left in place so a re-run resumes instead of re-embedding.
            summary["failed"] += 1
            logger.exception("embedding_migration: %s failed (%s)", p.name, exc)
    logger.info("embedding_migration: done in %.0fs - %s", time.monotonic() - started, summary)
    return summary


def start_background(
    client: QdrantClient,
    embeddings_for: Callable[[str], Any],
    retry_delay: int = RETRY_DELAY_SECONDS,
) -> threading.Thread:
    """Run the rebuild on a daemon thread so startup is not held up by it.

    A full re-embed takes hours. Blocking startup for that would keep the
    service out of readiness the whole time, and searches against a collection
    still queued simply return nothing until its turn comes — the same state
    they were already in.
    """

    def _worker() -> None:
        delay = retry_delay
        for attempt in range(1, RETRY_ATTEMPTS + 1):
            try:
                summary = run(client, embeddings_for)
                if summary.get("failed"):
                    # Retrying is cheap: register() skips whatever already
                    # carries the target model, so a retry only picks up what
                    # is still outstanding. Returning here instead would leave
                    # those collections unsearchable until the next restart.
                    raise RuntimeError(f"{summary['failed']} collection(s) failed to rebuild")
                return
            except Exception as exc:  # never take the process down from a daemon thread
                if attempt == RETRY_ATTEMPTS:
                    logger.exception(
                        "embedding_migration: giving up after %d attempts (%s); collections whose "
                        "model changed are still on their old vectors",
                        attempt,
                        exc,
                    )
                    return
                logger.warning("embedding_migration: attempt %d failed (%s); retrying in %ds", attempt, exc, delay)
                time.sleep(delay)
                delay = min(delay * 2, RETRY_MAX_DELAY_SECONDS)

    thread = threading.Thread(target=_worker, daemon=True, name="embedding-migration")
    thread.start()
    return thread
