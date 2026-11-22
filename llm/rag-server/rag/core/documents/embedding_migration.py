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

Concurrency: each collection has its own Postgres advisory lock, so any number of
workers -- server replicas, or ``python -m rag.core.documents.embedding_migration``
-- share the job. Each worker stages into a copy of its own, a copy is marked
complete before the swap, and only a complete copy may replace a missing source.

Gated behind ``RAG_EMBEDDING_MIGRATION_ENABLED``. It defaults ON only for the
on-device provider, which is also the default provider: an install that
inherits it has vectors from some other model, and for those the rewrite is
what keeps retrieval working at all. Any explicitly chosen provider was picked
against known data and keeps the opt-in, because this rewrites every
collection.
"""

import argparse
import contextlib
import hashlib
import logging
import os
import re
import socket
import threading
import time
import uuid
from typing import Any, Callable, Dict, Iterator, List, NamedTuple, Optional, Tuple

from qdrant_client import QdrantClient
from qdrant_client.models import Distance, PointStruct, VectorParams

from rag.core.documents.figure_cache import MODULE as FIGURE_MODULE
from rag.core.documents.source_store import MODULE as SOURCE_MODULE
from rag.core.types import Document

logger = logging.getLogger(__name__)


class MigrationUnavailable(RuntimeError):
    """The lock could not be evaluated at all -- Postgres was unreachable.

    Distinct from "another replica holds it", which is a healthy outcome that
    needs no retry. Collapsing the two would let a database blip look like a
    successful skip and leave the rebuild permanently undone.
    """


# Collection-metadata key holding the model that produced the vectors.
MODEL_KEY = "embedding_model"
# Set on a staging collection once it holds every point of its source. Only a
# staging marked complete may stand in for a source that no longer exists.
COMPLETE_KEY = "embmig_complete"

# Collections that keep their payloads behind a fixed placeholder vector rather than
# embeddings. Rebuilt at the embedder's dimension they can no longer be written to, and
# both of them swallow the resulting upsert failure — so the store silently stays empty.
#
# Recognised two ways on purpose. The dimension is the durable rule: an embedding index is
# never one-dimensional, so a placeholder store added later is excluded without anyone
# having to remember this list. The module names catch the ones a previous run already
# rebuilt to 384 or 1536, which no longer look like placeholder storage from the outside.
PLACEHOLDER_VECTOR_SIZE = 1
PLACEHOLDER_MODULES = {SOURCE_MODULE, FIGURE_MODULE}

# Small batches keep each upsert inside the default httpx read timeout; the
# retag migration hit intermittent ReadTimeouts at 128 points per upsert.
SCROLL_BATCH = 64
EMBED_BATCH = 32
EMBED_CACHE_MAX = 5000
DRAIN_MAX_PASSES = 20
STAGING_SUFFIX = "__embmig"


def _worker_id() -> str:
    raw = f"{socket.gethostname()}-{os.getpid()}".lower()
    return re.sub(r"[^a-z0-9-]", "-", raw)[-48:]


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
# pg_locks splits a bigint advisory key across (classid = key >> 32,
# objid = key & 0xFFFFFFFF); objsubid is 1 for the single-argument form of
# pg_try_advisory_lock. Both columns are type oid (unsigned 32-bit), so the
# unsigned values are what match; no sign conversion is needed.

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
    """The single migration-wide lock, used when per-collection locking is off."""
    with _advisory_lock(LOCK_KEY, "the migration") as still_held:
        yield still_held


@contextlib.contextmanager
def _shared_run_lock() -> Iterator[Optional[Callable[[], bool]]]:
    """The migration-wide lock in shared mode: compatible with other workers of
    this version, exclusive of a worker of the previous one."""
    with _advisory_lock(LOCK_KEY, "the migration (an older worker is running)", shared=True) as held:
        yield held


def _collection_lock_key(name: str) -> int:
    """A bigint advisory-lock key for one collection, stable across processes.

    hashlib, not hash(): Python salts str hashes per process, and every worker
    must ask for the same key for the same collection.
    """
    digest = hashlib.blake2b(f"embmig:{name}".encode(), digest_size=8).digest()
    key = int.from_bytes(digest, "big") & 0x7FFFFFFFFFFFFFFF
    return key if key != LOCK_KEY else key ^ 1


@contextlib.contextmanager
def _collection_lock(name: str) -> Iterator[Optional[Callable[[], bool]]]:
    """Yield a liveness check if this worker now owns ``name``, else None."""
    with _advisory_lock(_collection_lock_key(name), name) as still_held:
        yield still_held


@contextlib.contextmanager
def _advisory_lock(key: int, what: str, shared: bool = False) -> Iterator[Optional[Callable[[], bool]]]:
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
        take = "pg_try_advisory_lock_shared" if shared else "pg_try_advisory_lock"
        acquired = bool(conn.execute(text(f"SELECT {take}(:k)"), {"k": key}).scalar())
        if not acquired:
            logger.info("embedding_migration: another worker holds the lock for %s, skipping it", what)
    except Exception as exc:
        # Deliberately not swallowed: the caller has to be able to tell a
        # database failure from a healthy skip so it can retry.
        with contextlib.suppress(Exception):
            if conn is not None:
                conn.close()
        raise MigrationUnavailable(f"could not evaluate the migration lock: {exc}") from exc

    def still_held() -> bool:
        try:
            held = bool(conn.execute(text(_STILL_HELD_SQL), {"classid": key >> 32, "objid": key & 0xFFFFFFFF}).scalar())
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
                    release = "pg_advisory_unlock_shared" if shared else "pg_advisory_unlock"
                    conn.execute(text(f"SELECT {release}(:k)"), {"k": key})
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
    points: int = 0


def is_enabled() -> bool:
    """On by default for the on-device provider, opt-in for every other one.

    On-device is also the default *provider*, so an install that inherits it
    holds vectors built by whatever it used before. Leaving those unmigrated is
    not a rebuild deferred, it is retrieval silently returning nothing: the
    query lands in a space the stored points are not in. Hours of background
    rebuilding is the cheaper failure, so that case opts itself in.

    Every other provider is a deliberate choice made against known data, and
    keeps the explicit opt-in the rewrite deserves.

    An explicit RAG_EMBEDDING_MIGRATION_ENABLED wins in both directions.
    """
    from utils.config import Config

    implied = Config.embeddings_provider.strip().lower() == "ondevice"
    configured = os.environ.get("RAG_EMBEDDING_MIGRATION_ENABLED", "true" if implied else "false")
    return configured.strip().lower() == "true"


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


def _embedding_text_of(payload: Optional[Dict[str, Any]]) -> str:
    """The text a point's vector is computed from: its chunk_context before the
    page_content, the same as at ingest (Document.retrieval_text), so a rebuilt
    vector matches one a fresh sync would write."""
    metadata = payload.get("metadata") if isinstance(payload, dict) else None
    return Document(
        page_content=_text_of(payload), metadata=metadata if isinstance(metadata, dict) else {}
    ).retrieval_text()


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
    module = metadata.get("module")
    if params.size == PLACEHOLDER_VECTOR_SIZE or module in PLACEHOLDER_MODULES:
        logger.info("embedding_migration: %s is placeholder storage (module=%s), not an index; skipping", name, module)
        return None
    return params.size, params.distance, metadata, info.points_count or 0


def _staging_source(name: str) -> Optional[str]:
    """The collection a staging collection is rebuilding, or None if not staging.

    ``<src>__embmig`` is the single-worker name earlier versions used;
    ``<src>__embmig__<worker>`` is one worker's copy.
    """
    idx = name.find(STAGING_SUFFIX)
    return name[:idx] if idx > 0 else None


def _survey(client: QdrantClient, only: Optional[Callable[[str], bool]] = None) -> Tuple[List[Any], set, set]:
    """Collections, the active names, and the sources with leftover staging."""
    cols = list(client.get_collections().collections)
    if only is not None:
        cols = [c for c in cols if only(_staging_source(c.name) or c.name)]
    active = {c.name for c in cols if _staging_source(c.name) is None}
    interrupted = {src for c in cols if (src := _staging_source(c.name)) is not None}
    return cols, active, interrupted


def _stagings_for(client: QdrantClient, src: str) -> List[str]:
    return [c.name for c in client.get_collections().collections if _staging_source(c.name) == src]


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
    staging = _restorable_staging(client, src)
    if staging is None:
        logger.warning("embedding_migration: %s exists only as staging, and none of it is known complete", src)
        return None
    described = _describe(client, staging)
    if described is None:
        return None
    dim, distance, metadata, points = described
    metadata = {k: v for k, v in metadata.items() if k != COMPLETE_KEY}
    return Pending(
        src,
        _account_of(src),
        "interrupted",
        str(metadata.get(MODEL_KEY) or ""),
        dim,
        distance,
        metadata,
        points,
    )


def register(
    client: QdrantClient,
    embeddings_for: Callable[[str], Any],
    only: Optional[Callable[[str], bool]] = None,
) -> List[Pending]:
    """Decide what needs rebuilding. Reads metadata only — no embedding work.

    ``only`` limits the run to matching collection names. It is applied before
    anything is looked up, so a narrow run does not pay for the whole store.
    """
    pending: List[Pending] = []
    resolved: Dict[str, Optional[Tuple[str, int]]] = {}
    unstamped = 0

    cols, active, interrupted = _survey(client, only)
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
        if _staging_source(name) is not None:
            continue
        try:
            described = _describe(client, name)
            if described is None:
                continue
            dim, distance, metadata, points = described
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
                Pending(
                    name,
                    account,
                    stored_model or "unstamped",
                    target_model,
                    target_dim,
                    distance,
                    metadata,
                    points,
                )
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
    # Shared collections first: every account's searches reach them. Then the
    # smallest, so the most collections come back soonest.
    pending.sort(key=lambda p: (p.account != "", p.points, p.name))
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


def _embed_unique(embeddings: Any, texts: List[str], cache: Dict[str, List[float]], source: str) -> List[List[float]]:
    """Vectors for ``texts``, embedding each distinct text once per collection.

    Some collections hold the same text many times over (recommendation
    collections in prod: ~5 copies of each), and re-embedding every copy is
    most of the cost of rebuilding them.
    """
    missing = list(dict.fromkeys(t for t in texts if t not in cache))
    for i in range(0, len(missing), EMBED_BATCH):
        chunk = missing[i : i + EMBED_BATCH]
        vectors = embeddings.embed_documents(chunk)
        if len(vectors) != len(chunk):
            # zip() would silently truncate or pair payloads with the wrong
            # vectors, and the original collection is deleted once staging
            # looks complete -- so a short batch has to stop this collection,
            # not quietly shrink it.
            raise RuntimeError(f"embedding returned {len(vectors)} vectors for {len(chunk)} documents in {source}")
        cache.update(zip(chunk, vectors))
    result = [cache[t] for t in texts]
    # Each cached vector is a few KB of Python floats; an unbounded cache on a
    # large collection would exhaust the pod. Oldest go first (dicts keep
    # insertion order), so recently seen text stays cached.
    while len(cache) > EMBED_CACHE_MAX:
        cache.pop(next(iter(cache)))
    return result


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
        metadata=_final_metadata(p),
    )
    moved, offset = 0, None
    cache: Dict[str, List[float]] = {}
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
        if usable:
            vectors = _embed_unique(embeddings, [_embedding_text_of(pt.payload) for pt in usable], cache, source)
            client.upsert(
                collection_name=target,
                points=[PointStruct(id=pt.id, vector=v, payload=pt.payload) for pt, v in zip(usable, vectors)],
                wait=True,
            )
            moved += len(usable)
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


def _final_metadata(p: Pending) -> Dict[str, Any]:
    metadata = {k: v for k, v in p.metadata.items() if k != COMPLETE_KEY}
    metadata[MODEL_KEY] = p.target_model
    return metadata


def _staging_state(client: QdrantClient, name: str) -> Optional[Tuple[int, bool, str, Optional[int]]]:
    """(points, marked complete, model, dimension) for a staging collection, or None."""
    try:
        info = client.get_collection(name)
    except Exception:
        return None
    metadata = getattr(info.config, "metadata", None) or {}
    dim = getattr(info.config.params.vectors, "size", None)
    return info.points_count or 0, metadata.get(COMPLETE_KEY) is not None, str(metadata.get(MODEL_KEY) or ""), dim


def _reusable_staging(client: QdrantClient, p: Pending, expected: int, source_is_partial: bool) -> Optional[str]:
    """A staging copy of ``p.name`` that already holds every point, if any.

    Only a copy built for this target qualifies -- same model, same dimension.
    One left by a crashed run toward another model would otherwise be swapped in
    and stamped with this one: wrong vectors, no error.

    A copy marked complete wins, the fullest first. When the source is itself a
    partial result -- a crash mid copy-back -- its point count proves nothing,
    so an unmarked copy is accepted only under the single-worker name earlier
    versions used: they dropped the original only once that copy was full.
    Otherwise an unmarked copy qualifies by holding at least as many points as
    the source.
    """
    src = p.name
    marked: List[Tuple[int, str]] = []
    unmarked: List[Tuple[int, str]] = []
    for name in _stagings_for(client, src):
        state = _staging_state(client, name)
        if state is None or state[2] != p.target_model or state[3] != p.target_dim:
            continue
        (marked if state[1] else unmarked).append((state[0], name))
    if marked:
        return max(marked)[1]
    if source_is_partial:
        legacy = [c for c in unmarked if c[1] == f"{src}{STAGING_SUFFIX}"]
        return max(legacy)[1] if legacy else None
    if expected > 0:
        fits = [c for c in unmarked if c[0] >= expected]
        if fits:
            return max(fits)[1]
    return None


def _restorable_staging(client: QdrantClient, src: str) -> Optional[str]:
    """The staging copy to restore ``src`` from once the original is gone.

    Only a copy marked complete qualifies: a worker that lost its lock can leave
    a partial copy of its own behind. The single-worker name earlier versions
    used is accepted as well -- they dropped the original only after staging
    was full.
    """
    best: Optional[Tuple[str, int]] = None
    for name in _stagings_for(client, src):
        state = _staging_state(client, name)
        if state is None or not (state[1] or name == f"{src}{STAGING_SUFFIX}"):
            continue
        if best is None or state[0] > best[1]:
            best = (name, state[0])
    return best[0] if best else None


def _catch_up(
    client: QdrantClient,
    source: str,
    staging: str,
    load_embeddings: Callable[[], Any],
    still_held: Callable[[], bool],
) -> int:
    """Re-embed points written to ``source`` after staging had read past them.

    Writes keep landing in the source while it is being rebuilt. Without this
    pass they would be dropped with the source at the swap.
    """
    added, offset, embeddings = 0, None, None
    cache: Dict[str, List[float]] = {}
    while True:
        _still_mine(still_held, f"catching up {source}")
        # Ids only: nearly every point is already staged, and payloads are what
        # makes a pass over a large collection expensive.
        points, offset = client.scroll(
            collection_name=source, limit=SCROLL_BATCH, offset=offset, with_payload=False, with_vectors=False
        )
        if not points:
            break
        have = {
            r.id
            for r in client.retrieve(
                collection_name=staging, ids=[pt.id for pt in points], with_payload=False, with_vectors=False
            )
        }
        missing_ids = [pt.id for pt in points if pt.id not in have]
        missing = []
        if missing_ids:
            fetched = client.retrieve(collection_name=source, ids=missing_ids, with_payload=True, with_vectors=False)
            missing = [pt for pt in fetched if _text_of(pt.payload).strip()]
        if missing:
            embeddings = embeddings or load_embeddings()
            vectors = _embed_unique(embeddings, [_embedding_text_of(pt.payload) for pt in missing], cache, source)
            client.upsert(
                collection_name=staging,
                points=[PointStruct(id=pt.id, vector=v, payload=pt.payload) for pt, v in zip(missing, vectors)],
                wait=True,
            )
            added += len(missing)
        if offset is None:
            break
    if added:
        logger.info("embedding_migration: caught up %d point(s) written to %s during its rebuild", added, source)
    return added


def _drain(
    client: QdrantClient,
    source: str,
    staging: str,
    load_embeddings: Callable[[], Any],
    still_held: Callable[[], bool],
) -> None:
    """Catch up until a pass finds nothing new, then return for an immediate swap.

    One pass is not enough while writes keep arriving: a point written behind
    the scroll position, or after the pass ends, is dropped with the source.
    The point count is compared as well, because new ids are random and land
    anywhere in scroll order. What remains is the moment between the last count
    and the delete.
    """
    for _ in range(DRAIN_MAX_PASSES):
        before = client.get_collection(source).points_count or 0
        added = _catch_up(client, source, staging, load_embeddings, still_held)
        if not added and (client.get_collection(source).points_count or 0) == before:
            return
    # Writes that never pause would hold the swap off forever. Each pass just
    # caught up, so swapping now leaves the same window a quiet finish does.
    logger.warning(
        "embedding_migration: writes to %s kept arriving through %d catch-up passes; swapping now",
        source,
        DRAIN_MAX_PASSES,
    )


def rebuild(
    client: QdrantClient,
    p: Pending,
    load_embeddings: Callable[[], Any],
    still_held: Callable[[], bool] = lambda: True,
    worker: Optional[str] = None,
) -> str:
    """Rebuild one registered collection. Returns a status word.

    The caller holds this collection's lock. ``load_embeddings`` is a factory,
    only called when something actually has to be re-embedded: restoring an
    orphan or resuming complete staging needs no embeddings at all, and those
    are the recovery paths that should still work while the provider is down.
    """
    try:
        info = client.get_collection(p.name)
        source_exists = True
        expected = info.points_count or 0
        stored = str((getattr(info.config, "metadata", None) or {}).get(MODEL_KEY) or "")
    except Exception:
        # Restoring an orphan: the original was already dropped.
        source_exists, expected, stored = False, 0, ""

    if source_exists and stored == p.target_model and not _stagings_for(client, p.name):
        # Registration is a snapshot; another worker finished this one since.
        logger.info("embedding_migration: %s is already on %s, skipping", p.name, p.target_model)
        return "already"

    if source_exists:
        # Stamped with the target but still carrying staging: a crash during the
        # copy-back left it half restored.
        partial = stored == p.target_model
        staging = _reusable_staging(client, p, expected, source_is_partial=partial)
        if staging is None and partial:
            # Rebuilding from a half-restored source would make the loss permanent.
            raise RuntimeError(f"{p.name} was left half restored and no staging copy of it is known complete")
    else:
        staging = _restorable_staging(client, p.name)
        if staging is None:
            raise RuntimeError(f"{p.name} is gone and no staging copy of it is known complete")

    if staging is not None:
        logger.info("embedding_migration: reusing staged rebuild %s for %s", staging, p.name)
    else:
        # Each worker stages into a copy of its own, so a worker that has lost its
        # lock without knowing it can only ever touch its own copy. Resolved per
        # call, not at import: a pre-forking server would hand every child the
        # parent's pid.
        staging = f"{p.name}{STAGING_SUFFIX}__{worker or _worker_id()}"
        moved = _rebuild_into(client, p.name, staging, load_embeddings(), p, still_held)
        logger.info("embedding_migration: staged %d/%d points for %s", moved, expected, p.name)

    if source_exists:
        _catch_up(client, p.name, staging, load_embeddings, still_held)
        staged = client.get_collection(staging).points_count or 0
        # Recorded before anything is destroyed, so a copy left behind by a run
        # that dies mid-swap is known to be whole.
        client.update_collection(collection_name=staging, metadata={COMPLETE_KEY: staged})

    # The last moment before anything is destroyed. Everything above this line
    # only added data; everything below removes the original.
    _still_mine(still_held, f"the swap for {p.name}")
    if source_exists:
        _drain(client, p.name, staging, load_embeddings, still_held)

    with contextlib.suppress(Exception):
        client.delete_collection(p.name)
    client.create_collection(
        p.name,
        vectors_config=VectorParams(size=p.target_dim, distance=p.distance),
        metadata=_final_metadata(p),
    )
    copied = _copy_vectors(client, staging, p.name, still_held)
    for leftover in _stagings_for(client, p.name):
        try:
            client.delete_collection(leftover)
        except Exception as exc:
            # The collection is already rebuilt and correct. A leftover copy is
            # picked up as interrupted next start: wasteful, not harmful.
            logger.warning(
                "embedding_migration: %s rebuilt, but its staging collection %s could not be removed (%s)",
                p.name,
                leftover,
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


def run(
    client: QdrantClient,
    embeddings_for: Callable[[str], Any],
    only: Optional[Callable[[str], bool]] = None,
) -> Dict[str, int]:
    """Register, then rebuild everything registered. Safe to call repeatedly.

    By default each collection is locked on its own, so any number of workers
    can share the job. With per-collection locking switched off, one lock covers
    the whole run, as before; see ``_exclusive_run``.
    """
    from utils.config import Config

    if Config.embedding_migration_per_collection_lock:
        # Workers of this version share the migration-wide lock; a worker of the
        # previous version takes it exclusively and knows nothing of the
        # per-collection locks. Holding it shared keeps the two from rebuilding
        # the same collection while a rollout replaces one with the other.
        with _shared_run_lock() as run_lock:
            if run_lock is None:
                raise RuntimeError("an older migration worker holds the migration lock; retrying later")
            return _run_locked(client, embeddings_for, run_lock, only, per_collection=True)
    with _exclusive_run() as still_held:
        if still_held is None:
            return {"registered": 0, "rebuilt": 0, "failed": 0, "skipped_locked": 1}
        return _run_locked(client, embeddings_for, still_held, only, per_collection=False)


def _both(a: Callable[[], bool], b: Callable[[], bool]) -> Callable[[], bool]:
    return lambda: a() and b()


def _run_locked(
    client: QdrantClient,
    embeddings_for: Callable[[str], Any],
    run_lock: Callable[[], bool],
    only: Optional[Callable[[str], bool]] = None,
    per_collection: bool = False,
) -> Dict[str, int]:
    """``run_lock`` is the migration-wide lock (exclusive, or shared when
    ``per_collection``); it is checked together with each collection's lock."""
    started = time.monotonic()
    pending = register(client, embeddings_for, only)
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
        if not run_lock():
            # Carrying on without the lock risks a second replica rebuilding the
            # same collection. Stopping leaves staging intact, so the next start
            # resumes rather than re-embedding.
            logger.warning("embedding_migration: stopping early, the migration lock is no longer held")
            summary["stopped_lock_lost"] = 1
            break
        with contextlib.ExitStack() as stack:
            still_held: Callable[[], bool] = run_lock
            if per_collection:
                collection_held = stack.enter_context(_collection_lock(p.name))
                if collection_held is None:
                    summary["skipped_locked"] = summary.get("skipped_locked", 0) + 1
                    continue
                # Losing the shared lock lets a worker of the previous version in,
                # which knows nothing of per-collection locks.
                still_held = _both(run_lock, collection_held)
            try:
                if rebuild(client, p, lambda: embeddings_for(p.account), still_held) == "rebuilt":
                    summary["rebuilt"] += 1
                else:
                    summary["already"] = summary.get("already", 0) + 1
            except Exception as exc:
                if not still_held():
                    # Not this collection's fault: the lock went away mid-rebuild
                    # and rebuild() stopped on purpose. Counting it as a failure
                    # and printing a traceback would misreport a deliberate stop.
                    logger.warning("embedding_migration: stopped %s, its lock is no longer held", p.name)
                    if not run_lock():
                        summary["stopped_lock_lost"] = 1
                        break
                    summary["stopped_lock_lost"] = summary.get("stopped_lock_lost", 0) + 1
                    continue
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


def main(argv: Optional[List[str]] = None) -> int:
    """Run the migration as a standalone worker, alongside or instead of the server.

    Workers share the job through per-collection locks in the same Postgres the
    server uses, so point this at that Postgres and that Qdrant.
    """
    parser = argparse.ArgumentParser(description="Re-embed Qdrant collections built with another model.")
    parser.add_argument("--shared-only", action="store_true", help="only collections shared by every account")
    parser.add_argument("--collections", default="", help="comma-separated collection names to limit the run to")
    args = parser.parse_args(argv)

    from config import setup_logger

    setup_logger()

    from rag.core.embeddings.generator import get_embeddings
    from rag.qdrant.client import get_qdrant_client
    from utils.config import Config

    if not Config.embedding_migration_per_collection_lock:
        parser.error("RAG_EMBEDDING_MIGRATION_PER_COLLECTION_LOCK must be on to run more than one worker")
    names = {n.strip() for n in args.collections.split(",") if n.strip()}

    def only(name: str) -> bool:
        return (not args.shared_only or _account_of(name) == "") and (not names or name in names)

    summary = run(get_qdrant_client(), get_embeddings, only)
    print(summary)
    return 1 if summary.get("failed") else 0


if __name__ == "__main__":
    raise SystemExit(main())
