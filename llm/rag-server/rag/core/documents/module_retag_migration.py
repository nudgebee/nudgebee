"""One-time startup migration: retag legacy ``docs`` / ``nudgebee_docs`` modules to
``knowledge_base`` so existing Qdrant data remains searchable after PR #28733.

Before PR #28733:
  - Confluence + ServiceNow KB ingests tagged documents with ``module="docs"`` and
    wrote into collections named ``<account_id>_docs``.
  - Nudgebee product docs ingest tagged with ``module="nudgebee_docs"`` in the
    global collection named ``nudgebee_docs``.

PR #28733 unifies all doc sources under ``module="knowledge_base"`` going forward
and the new llm-server UnifiedSearchAgent filters on that single module. Without
this migration, all pre-existing Qdrant data becomes invisible to the new search
(both the collection-level filter in ``_should_include_collection`` and the
point-payload filter in ``filters.py`` would miss it).

The migration:
  1. Lists every Qdrant collection (via the cached list_collections_optimized helper).
  2. Skips anything not tagged with a legacy module.
  3. Renames per-account collections ``<uuid>_docs`` → ``<uuid>_knowledge_base``
     (copies points, updates per-point payload, deletes old collection).
  4. For the global ``nudgebee_docs`` collection, keeps the name but rewrites
     collection-level metadata + per-point payload module to ``knowledge_base``.
  5. Invalidates the collection list and metadata caches so subsequent reads see
     the new state.

Idempotent: a re-run does nothing because legacy-tagged collections will no longer
exist after step 3/4. Gated behind ``RAG_MODULE_RETAG_MIGRATION_ENABLED`` so it can
be turned off quickly in case of regressions.

Run via ``server.py`` startup hook under a FileLock so only one worker executes it.
"""

import logging
import os
import time
from typing import Any, Dict, List, Optional, Tuple

from qdrant_client import QdrantClient
from qdrant_client.http.exceptions import ResponseHandlingException
from qdrant_client.models import Distance, HnswConfigDiff, PointStruct, VectorParams

logger = logging.getLogger(__name__)

LEGACY_MODULES = {"docs", "nudgebee_docs"}
TARGET_MODULE = "knowledge_base"

# Collections whose name should stay the same and only have metadata updated.
# The global nudgebee_docs collection is referenced by explicit name in the
# scraper even after this PR, so we must keep it rather than rename to
# ``knowledge_base``.
NAME_PRESERVED_COLLECTIONS = {"nudgebee_docs"}

# Batches kept deliberately small so each Qdrant upsert stays within the
# default httpx read timeout. Earlier local runs at 128 points/upsert
# produced intermittent ``ReadTimeout`` on ``nudgebee_docs`` re-upserts
# (~35s per batch for 1024-dim vectors), which in the delete-then-upsert
# path caused data loss. 50 keeps per-request work small enough to finish
# inside the default timeout on constrained dev machines.
SCROLL_BATCH = 50

# Suffix used on the temporary staging collection created during the
# "name-preserved" migration. Kept distinctive + hard to collide with
# a real collection name so operators can spot leftover staging state
# after a crashed run.
STAGING_SUFFIX = "__retag_staging"

# Upsert retry policy: on ResponseHandlingException (Qdrant client's wrapper
# around httpx timeouts / connection errors) we retry up to this many times
# with exponential backoff before giving up. The staging-first migration
# relies on this so transient Qdrant hiccups don't destroy data.
UPSERT_MAX_RETRIES = 3
UPSERT_RETRY_INITIAL_BACKOFF_SEC = 2.0


def _target_collection_name(source_name: str) -> str:
    """Derive the target collection name for a legacy-tagged collection.

    Keeps ``nudgebee_docs`` as-is so the scraper's hard-coded collection name
    still resolves after migration. Per-account collections ending in
    ``_docs`` or ``_nudgebee_docs`` are renamed to ``_knowledge_base``.
    Anything else falls through unchanged so only the metadata is rewritten.
    """
    if source_name in NAME_PRESERVED_COLLECTIONS:
        return source_name
    if source_name.endswith("_nudgebee_docs"):
        return source_name[: -len("_nudgebee_docs")] + "_knowledge_base"
    if source_name.endswith("_docs"):
        return source_name[: -len("_docs")] + "_knowledge_base"
    return source_name


def _get_vector_config(client: QdrantClient, collection_name: str) -> Tuple[int, Distance]:
    """Pull (size, distance) from the source collection so we can recreate a
    matching one. Defaults to cosine + 1024 if introspection fails, matching
    the titan-embed-text-v2 default — this is a migration safety net, not a
    silent override: we log loudly and proceed."""
    try:
        info = client.get_collection(collection_name)
        vectors = info.config.params.vectors
        # Qdrant represents unnamed vector config as a bare VectorParams,
        # and named-vector configs as a dict of name→VectorParams. The
        # migration assumes the unnamed (default) shape — named vectors
        # are out of scope since the ingest path never writes them.
        if isinstance(vectors, VectorParams):
            return vectors.size, vectors.distance
        logger.warning("retag_migration: %s uses named-vector config; falling back to defaults", collection_name)
    except Exception as exc:
        logger.warning("retag_migration: failed to read vector config for %s: %s", collection_name, exc)
    return 1024, Distance.COSINE


def _upsert_with_retry(client: QdrantClient, collection_name: str, points: List[PointStruct]) -> None:
    """Upsert with retries on transient Qdrant timeouts.

    The staging-first in-place migration relies on every upsert eventually
    succeeding — a permanent failure here means data lives only in the staging
    collection and the operator must intervene. Retries smooth over brief
    overloads (e.g. Qdrant's WAL flushing) without escalating to data loss.
    """
    attempt = 0
    backoff = UPSERT_RETRY_INITIAL_BACKOFF_SEC
    while True:
        try:
            client.upsert(collection_name=collection_name, points=points, wait=True)
            return
        except ResponseHandlingException as exc:
            attempt += 1
            if attempt > UPSERT_MAX_RETRIES:
                logger.error(
                    "retag_migration: upsert into %s failed after %d attempts, giving up",
                    collection_name,
                    UPSERT_MAX_RETRIES,
                )
                raise
            logger.warning(
                "retag_migration: upsert into %s failed (attempt %d/%d): %s — retrying in %.1fs",
                collection_name,
                attempt,
                UPSERT_MAX_RETRIES,
                exc,
                backoff,
            )
            time.sleep(backoff)
            backoff *= 2


def _retag_points(points: Any) -> List[PointStruct]:
    """Materialise scrolled ``Record`` objects into ``PointStruct`` with the
    ``metadata.module`` rewritten to ``knowledge_base``. Skips points with no
    vector (defensive — shouldn't happen since callers always pass
    ``with_vectors=True``)."""
    retagged: List[PointStruct] = []
    for p in points:
        payload = dict(p.payload or {})
        metadata = dict(payload.get("metadata") or {})
        if metadata.get("module") in LEGACY_MODULES:
            metadata["module"] = TARGET_MODULE
            payload["metadata"] = metadata
        if p.vector is None:
            logger.warning("retag_migration: skipping point %s with no vector", p.id)
            continue
        retagged.append(PointStruct(id=p.id, vector=p.vector, payload=payload))
    return retagged


def _scroll_and_retag_points(
    client: QdrantClient, source_name: str, target_name: str, batch_size: int = SCROLL_BATCH
) -> int:
    """Copy every point from source → target, rewriting the ``module`` field in
    the payload's ``metadata`` dict. Preserves point IDs so a partial run that
    gets retried produces the same result (no duplicates).

    Returns the total number of points migrated.
    """
    migrated = 0
    next_offset: Optional[Any] = None
    while True:
        points, next_offset = client.scroll(
            collection_name=source_name,
            limit=batch_size,
            offset=next_offset,
            with_payload=True,
            with_vectors=True,
        )
        if not points:
            break
        retagged = _retag_points(points)
        if retagged:
            _upsert_with_retry(client, target_name, retagged)
        migrated += len(retagged)
        logger.info("retag_migration: %s → %s migrated=%d", source_name, target_name, migrated)
        if next_offset is None:
            break
    return migrated


def _copy_collection(client: QdrantClient, source_name: str, target_name: str, batch_size: int = SCROLL_BATCH) -> int:
    """Copy all points from ``source_name`` to ``target_name`` preserving IDs.
    No payload rewriting — used by the staging→source restore phase where
    the retag has already been applied during the source→staging phase."""
    copied = 0
    next_offset: Optional[Any] = None
    while True:
        points, next_offset = client.scroll(
            collection_name=source_name,
            limit=batch_size,
            offset=next_offset,
            with_payload=True,
            with_vectors=True,
        )
        if not points:
            break
        to_write: List[PointStruct] = []
        for p in points:
            if p.vector is None:
                logger.warning("retag_migration: skipping point %s with no vector during copy", p.id)
                continue
            to_write.append(PointStruct(id=p.id, vector=p.vector, payload=p.payload or {}))  # type: ignore[arg-type]
        if to_write:
            _upsert_with_retry(client, target_name, to_write)
        copied += len(to_write)
        if next_offset is None:
            break
    return copied


def _safe_count(client: QdrantClient, collection_name: str) -> int:
    """Exact point count, or -1 if the collection is missing. We use exact
    counts for the staging/source invariant checks — an approximate count
    from Qdrant can legitimately lag for seconds after a large upsert and
    would produce false mismatches."""
    try:
        return client.count(collection_name=collection_name, exact=True).count
    except Exception as exc:
        logger.warning("retag_migration: count failed for %s: %s", collection_name, exc)
        return -1


def _ensure_target_collection(
    client: QdrantClient,
    target_name: str,
    vector_size: int,
    distance: Distance,
    source_metadata: Dict[str, Any],
) -> None:
    """Create the target collection preserving every source metadata field
    except ``module`` (overwritten to ``knowledge_base``). If the target
    already exists we leave it alone — subsequent ``upsert`` calls merge
    points in, which is the key idempotency handle for a crashed-mid-run
    migration.

    Preserving ``account`` is load-bearing: the search filter in
    ``rag/core/llm/rag.py:_filter_collections_for_module_and_account``
    requires ``metadata["account"]`` to match the query account or
    ``"global"``. A collection with no ``account`` key satisfies neither
    branch and is silently filtered out, so a rename that drops the field
    would appear to succeed but leave every subsequent search returning
    zero hits (#28733 review feedback)."""
    try:
        client.get_collection(target_name)
        logger.info("retag_migration: target collection %s already exists, will upsert into it", target_name)
        return
    except Exception:
        pass  # not found, we'll create it

    new_metadata = {
        "module": TARGET_MODULE,
        **{k: v for k, v in source_metadata.items() if k != "module"},
    }
    client.create_collection(
        collection_name=target_name,
        vectors_config=VectorParams(size=vector_size, distance=distance, on_disk=True),
        hnsw_config=HnswConfigDiff(on_disk=True),
        metadata=new_metadata,
    )
    logger.info("retag_migration: created target collection %s with metadata=%s", target_name, new_metadata)


def _collection_exists(client: QdrantClient, name: str) -> bool:
    try:
        client.get_collection(name)
        return True
    except Exception:
        return False


def _migrate_in_place_via_staging(client: QdrantClient, source_name: str, source_metadata: Dict[str, Any]) -> int:
    """Safe in-place retag using a staging collection so data is never
    simultaneously absent from both source and staging.

    Invariant: at every point in time at least one of {source, staging}
    holds a full copy of the data, so a process crash mid-flight can
    always be recovered by re-running the migration.

    Phases:
      1. Copy source → staging, retagging payloads along the way.
         (Source is the authoritative copy until staging is verified.)
      2. Verify staging.count == source.count. Abort if mismatch.
      3. Delete source. Staging now holds the authoritative copy.
      4. Recreate source with new collection-level metadata.
      5. Copy staging → source (payload already retagged in phase 1).
      6. Verify source.count == staging.count.
      7. Delete staging.

    On restart with staging already populated (prior run crashed):
      - If source exists and staging exists: phase 1 is re-entered; upsert
        is idempotent on same IDs so re-runs converge to the correct count.
      - If source missing and staging exists: we can skip to phase 4 and
        reuse what's in staging. This happens when a prior run crashed
        after phase 3.
    """
    vector_size, distance = _get_vector_config(client, source_name)
    staging_name = source_name + STAGING_SUFFIX
    new_collection_metadata = {
        "module": TARGET_MODULE,
        **{k: v for k, v in source_metadata.items() if k != "module"},
    }

    source_exists = _collection_exists(client, source_name)
    staging_exists = _collection_exists(client, staging_name)

    if not source_exists and not staging_exists:
        # Caller shouldn't have reached here — legacy detection needed at
        # least the source collection. Log and exit without doing damage.
        logger.warning("retag_migration: %s has neither source nor staging, nothing to do", source_name)
        return 0

    # Phase 1-2: populate staging from source, unless we're recovering
    # from a crash that already deleted the source.
    if source_exists:
        if not staging_exists:
            client.create_collection(
                collection_name=staging_name,
                vectors_config=VectorParams(size=vector_size, distance=distance, on_disk=True),
                hnsw_config=HnswConfigDiff(on_disk=True),
                metadata=new_collection_metadata,
            )
            logger.info("retag_migration: created staging collection %s", staging_name)
        else:
            logger.info(
                "retag_migration: staging %s already exists from prior run, re-scrolling source to converge",
                staging_name,
            )

        copied = _scroll_and_retag_points(client, source_name, staging_name)
        src_count = _safe_count(client, source_name)
        stg_count = _safe_count(client, staging_name)
        # Staging may have more rows than source if this is a retry whose
        # prior run had already copied some points — so we require staging
        # to be AT LEAST source, not strictly equal. Fewer points in staging
        # is a genuine data-integrity red flag.
        if stg_count < src_count:
            raise RuntimeError(
                f"retag_migration: staging {staging_name} has {stg_count} points "
                f"but source {source_name} has {src_count} — aborting before destructive phase"
            )
        logger.info(
            "retag_migration: phase 1/2 complete for %s: source=%d staging=%d copied=%d",
            source_name,
            src_count,
            stg_count,
            copied,
        )

        # Phase 3: source is now redundant. Delete it.
        client.delete_collection(source_name)
        logger.info("retag_migration: phase 3 — deleted source %s (staging is authoritative)", source_name)

    # Phase 4: recreate source with new metadata.
    client.create_collection(
        collection_name=source_name,
        vectors_config=VectorParams(size=vector_size, distance=distance, on_disk=True),
        hnsw_config=HnswConfigDiff(on_disk=True),
        metadata=new_collection_metadata,
    )
    logger.info("retag_migration: phase 4 — recreated %s with module=%s", source_name, TARGET_MODULE)

    # Phase 5: copy staging back into the recreated source. Payloads were
    # already retagged during phase 1, so a plain copy (no transformation)
    # is correct here.
    restored = _copy_collection(client, staging_name, source_name)
    logger.info("retag_migration: phase 5 — restored %d points into %s", restored, source_name)

    # Phase 6: verify final counts. A mismatch here means staging→source
    # lost data somewhere; we leave staging alone so the operator can
    # investigate and re-run.
    final_src = _safe_count(client, source_name)
    final_stg = _safe_count(client, staging_name)
    if final_src < final_stg:
        raise RuntimeError(
            f"retag_migration: after restore source={final_src} < staging={final_stg}, "
            f"leaving staging {staging_name} intact for recovery"
        )

    # Phase 7: migration complete — drop staging.
    client.delete_collection(staging_name)
    logger.info("retag_migration: phase 7 — deleted staging %s", staging_name)
    return final_src


def _migrate_single_collection(
    client: QdrantClient, source_name: str, source_metadata: Dict[str, Any]
) -> Dict[str, Any]:
    """Migrate one legacy-tagged collection.

    Behaviour:
      - Rename case (target != source): ensure target exists, copy points with
        retagged payload, then delete source. Idempotent on retry because the
        source remains authoritative until the final delete.
      - In-place case (target == source, e.g. ``nudgebee_docs``): delegate to
        ``_migrate_in_place_via_staging`` which keeps at least one full copy
        of the data at every step so crashes don't destroy it.
    """
    target_name = _target_collection_name(source_name)
    vector_size, distance = _get_vector_config(client, source_name)
    detail: Dict[str, Any] = {
        "source": source_name,
        "target": target_name,
        "points": 0,
        "mode": "rename" if target_name != source_name else "in_place",
    }

    if target_name != source_name:
        _ensure_target_collection(client, target_name, vector_size, distance, source_metadata)
        detail["points"] = _scroll_and_retag_points(client, source_name, target_name)
        client.delete_collection(source_name)
        logger.info("retag_migration: deleted source collection %s after rename", source_name)
        return detail

    detail["points"] = _migrate_in_place_via_staging(client, source_name, source_metadata)
    return detail


def retag_legacy_modules() -> Dict[str, Any]:
    """Entry point. Scans all collections and migrates any legacy-tagged ones.

    Callers in ``server.py`` are responsible for the FileLock guard — we don't
    acquire it here so this function can also be called directly from unit
    tests or ad-hoc scripts.

    Returns a summary dict for logging / operator inspection.
    """
    from rag.core.cache import get_collection_list_cache
    from rag.core.metadata_cache import get_metadata_cache
    from rag.qdrant.client import get_qdrant_client, list_collections_optimized

    client = get_qdrant_client()
    summary: Dict[str, Any] = {"scanned": 0, "migrated": [], "skipped": 0, "errors": []}

    collections = list_collections_optimized()
    summary["scanned"] = len(collections)

    for info in collections:
        metadata = info.metadata or {}
        module = metadata.get("module")
        if module not in LEGACY_MODULES:
            summary["skipped"] += 1
            continue
        try:
            detail = _migrate_single_collection(client, info.name, metadata)
            summary["migrated"].append(detail)
        except Exception as exc:
            # One failure must not block migration of the remaining collections.
            # Operator-visible error list is the signal to investigate and retry.
            logger.exception("retag_migration: failed for collection %s", info.name)
            summary["errors"].append({"collection": info.name, "error": str(exc)})

    # Caches held stale (pre-migration) CollectionInfo. Invalidating here forces
    # the next list_collections call to re-read from Qdrant, so the UI and the
    # llm-server filter see the new state immediately.
    get_collection_list_cache().invalidate()
    get_metadata_cache().invalidate()

    logger.info(
        "retag_migration: complete — scanned=%d migrated=%d skipped=%d errors=%d",
        summary["scanned"],
        len(summary["migrated"]),
        summary["skipped"],
        len(summary["errors"]),
    )
    return summary


def is_enabled() -> bool:
    """Migration is opt-in via env flag (default: on) so it runs only in
    environments an operator has explicitly greenlit — typically staged one
    cluster at a time. Omitting or setting the var to anything other than
    ``true`` keeps the migration dormant."""
    return os.environ.get("RAG_MODULE_RETAG_MIGRATION_ENABLED", "true").lower() == "true"
