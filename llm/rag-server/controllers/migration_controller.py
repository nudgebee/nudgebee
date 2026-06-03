"""
Migration Controller for Qdrant Operations.

Provides REST API endpoints for:
- Qdrant→Qdrant migration (embedded to standalone)
- RAG collection renaming (agent_* to kb_*)
"""

import logging
import threading

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/api/migrate")

# Global Qdrant→Qdrant migration tracking
_qdrant_migration_status = {"status": "not_started", "progress": {}}
_qdrant_migration_lock = threading.Lock()


# Qdrant to Qdrant Migration Endpoints


@router.post("/qdrant-to-qdrant/validate")
async def validate_qdrant_migration():
    """
    Validate Qdrant→Qdrant migration prerequisites.

    Returns:
        JSON response with validation results
    """
    try:
        from rag.migration.qdrant_to_qdrant_migration import QdrantToQdrantMigration

        logger.info("Validating Qdrant→Qdrant migration")
        migration = QdrantToQdrantMigration()
        validation = migration.validate_migration()

        return {"success": True, "validation": validation}

    except Exception as e:
        logger.error(f"Error validating Qdrant→Qdrant migration: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e))


@router.post("/qdrant-to-qdrant/start")
async def start_qdrant_migration(batch_size: int = 25, force: bool = False):
    """
    Start migration from embedded Qdrant to standalone Qdrant.

    This migrates all collections from local embedded Qdrant to standalone Qdrant server.
    Collections are automatically created with on_disk=True for both vectors and HNSW index.

    Uses conservative batch size and delays to prevent server overload and health check failures.
    Migration runs in background thread to keep health endpoint responsive.

    Args:
        batch_size: Number of points to migrate per batch (default: 25, conservative to prevent pod restarts)
        force: Force re-migration even if collection exists in target (default: false)

    Returns:
        JSON response with migration initiation status
    """
    try:
        from rag.migration.qdrant_to_qdrant_migration import QdrantToQdrantMigration

        # Check if migration is already in progress
        with _qdrant_migration_lock:
            if _qdrant_migration_status["status"] == "in_progress":
                raise HTTPException(
                    status_code=409,
                    detail={
                        "success": False,
                        "message": "Qdrant→Qdrant migration already in progress",
                        "current_status": _qdrant_migration_status,
                    },
                )

        logger.info(f"Starting Qdrant→Qdrant migration (batch_size={batch_size}, force={force})")

        # Run migration in background thread to keep health endpoint responsive
        def run_migration():
            global _qdrant_migration_status
            try:
                with _qdrant_migration_lock:
                    _qdrant_migration_status = {"status": "in_progress", "progress": {}}

                migration = QdrantToQdrantMigration()
                result = migration.migrate_all(batch_size=batch_size, force=force)

                with _qdrant_migration_lock:
                    _qdrant_migration_status = {"status": "completed", "progress": result}

                logger.info(f"Qdrant→Qdrant migration completed: {result}")
            except Exception as e:
                with _qdrant_migration_lock:
                    _qdrant_migration_status = {"status": "failed", "progress": {}, "error": str(e)}
                logger.error(f"Qdrant→Qdrant migration failed with exception: {e}", exc_info=True)

        migration_thread = threading.Thread(target=run_migration, daemon=True)
        migration_thread.start()

        return {
            "success": True,
            "message": "Qdrant→Qdrant migration started in background",
            "batch_size": batch_size,
            "force": force,
        }

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error starting Qdrant→Qdrant migration: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e))


@router.get("/qdrant-to-qdrant/status")
async def get_qdrant_migration_status():
    """
    Get current Qdrant→Qdrant migration status.

    Returns:
        JSON response with migration status and progress
    """
    try:
        with _qdrant_migration_lock:
            status = _qdrant_migration_status.copy()

        return {"success": True, "status": status}

    except Exception as e:
        logger.error(f"Error getting Qdrant→Qdrant migration status: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e))


@router.get("/qdrant-to-qdrant/stats")
async def get_qdrant_migration_stats():
    """
    Get statistics about embedded and standalone Qdrant.

    Returns:
        JSON response with stats from both source and target
    """
    try:
        from rag.migration.qdrant_to_qdrant_migration import QdrantToQdrantMigration

        migration = QdrantToQdrantMigration()
        source_stats = migration.get_source_stats()
        target_stats = migration.get_target_stats()

        return {"success": True, "source": source_stats, "target": target_stats}

    except Exception as e:
        logger.error(f"Error getting Qdrant→Qdrant stats: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e))


# RAG Collection Renaming Endpoints (agent_{id} → kb_{id})


class RAGCollectionRenameRequest(BaseModel):
    dry_run: bool = False
    keep_old_collections: bool = True  # Safety: keep old collections by default


@router.post("/rag-collections/get-mappings")
async def get_rag_collection_mappings():
    """
    Query database to get agent_id → kb_id mappings from V620 migration.

    After V620 migration runs, llm_kb_agent_mappings contains the mapping
    of which agent_* collections should be renamed to kb_* collections.

    Returns:
        JSON response with list of mappings (agent_id, kb_id, old/new collection names)
    """
    try:
        from sqlalchemy import text
        from rag.core.utils.db_query import engine

        logger.info("Fetching RAG collection migration mappings from database")

        with engine.connect() as connection:
            # Query llm_rags table - this is what exists BEFORE V620 migration runs
            # V620 will preserve rag.id as kb.id, so we can generate the new collection name
            # Collection naming: old format is {account_id}_{agent_id}, new format is kb_{kb_id}
            query = text("""
                SELECT
                    id,
                    account_id,
                    agent_id,
                    data_filename
                FROM llm_rags
                ORDER BY agent_id
                """)

            result = connection.execute(query)

            mappings = []
            for row in result:
                mappings.append(
                    {
                        "agent_id": row.agent_id,
                        "account_id": row.account_id,
                        "kb_id": row.id,  # This will become the KB ID after V620 migration
                        "kb_name": f"migrated_{row.agent_id}_{row.data_filename or 'rag'}",
                        "old_collection": f"{row.account_id}_{row.agent_id}",  # Format: {account_id}_{module}
                        "new_collection": f"kb_{row.id}",  # Format: kb_{kb_id} (UUID is globally unique)
                    }
                )

        logger.info(f"Found {len(mappings)} RAG collections to migrate")

        return {"success": True, "count": len(mappings), "mappings": mappings}

    except Exception as e:
        logger.error(f"Error fetching RAG collection mappings: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e))


@router.post("/rag-collections/rename")
async def rename_rag_collections(request: RAGCollectionRenameRequest):
    """
    Rename RAG collections from agent_{agent_id} to kb_{kb_id}.

    This endpoint:
    1. Fetches mappings from database
    2. For each mapping, creates an alias in Qdrant
    3. Optionally keeps old collections as backup

    Request body:
        - dry_run: boolean (default: false) - Only simulate, don't actually rename
        - keep_old_collections: boolean (default: true) - Keep old collections for rollback

    Returns:
        JSON response with migration results
    """
    try:
        import os
        from rag.qdrant import get_qdrant_client

        logger.info(
            f"Starting RAG collection rename (dry_run={request.dry_run}, keep_old={request.keep_old_collections})"
        )

        # Get vector DB type
        vector_db = os.getenv("VECTOR_DB", "qdrant").lower()

        if vector_db != "qdrant":
            raise HTTPException(
                status_code=400, detail=f"Collection renaming only supported for Qdrant, current VECTOR_DB={vector_db}"
            )

        # Get mappings from database
        mappings_response = await get_rag_collection_mappings()
        mappings = mappings_response["mappings"]

        if not mappings:
            return {"success": True, "message": "No collections to migrate", "renamed": [], "skipped": [], "errors": []}

        qdrant_client = get_qdrant_client()

        renamed = []
        skipped = []
        errors = []

        for mapping in mappings:
            old_name = mapping["old_collection"]
            new_name = mapping["new_collection"]

            try:
                # Check if old collection exists
                try:
                    old_collection = qdrant_client.get_collection(old_name)
                    old_count = old_collection.points_count
                except Exception:
                    logger.warning(f"Old collection {old_name} not found, skipping")
                    skipped.append({**mapping, "reason": "old_collection_not_found"})
                    continue

                # Check if new collection/alias already exists
                try:
                    new_collection = qdrant_client.get_collection(new_name)
                    new_count = new_collection.points_count
                    logger.warning(f"New collection {new_name} already exists with {new_count} points")
                    skipped.append(
                        {
                            **mapping,
                            "reason": "new_collection_already_exists",
                            "new_collection_points": new_count,
                        }
                    )
                    continue
                except Exception:
                    pass  # Good, new collection doesn't exist

                if request.dry_run:
                    logger.info(f"[DRY RUN] Would create alias {new_name} → {old_name} ({old_count} points)")
                    renamed.append({**mapping, "points_count": old_count, "dry_run": True})
                else:
                    # Actual renaming: use collection alias
                    # Qdrant supports aliases which is safer than recreating
                    logger.info(f"Creating alias {new_name} → {old_name}")

                    # Create alias pointing to old collection
                    qdrant_client.update_collection_aliases(
                        change_aliases_operations=[
                            {"create_alias": {"collection_name": old_name, "alias_name": new_name}}
                        ]
                    )

                    logger.info(f"✓ Created alias {new_name} → {old_name} ({old_count} points)")

                    renamed.append({**mapping, "points_count": old_count, "method": "alias", "dry_run": False})

                    # Optionally delete old collection (not recommended initially)
                    if not request.keep_old_collections:
                        logger.warning(f"Deleting old collection {old_name} (keep_old_collections=false)")
                        qdrant_client.delete_collection(old_name)

            except Exception as e:
                logger.error(f"Error renaming {old_name} → {new_name}: {e}", exc_info=True)
                errors.append({**mapping, "error": str(e)})

        result = {
            "success": len(errors) == 0,
            "dry_run": request.dry_run,
            "total": len(mappings),
            "renamed_count": len(renamed),
            "skipped_count": len(skipped),
            "error_count": len(errors),
            "renamed": renamed,
            "skipped": skipped,
            "errors": errors,
        }

        if request.dry_run:
            result["message"] = f"Dry run complete: {len(renamed)} collections would be renamed using aliases"
        else:
            result["message"] = f"Migration complete: {len(renamed)} collections renamed using aliases"
            if request.keep_old_collections:
                result["note"] = (
                    "Old collections kept for safety. Use /rag-collections/cleanup to remove them after verification."
                )

        return result

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error renaming RAG collections: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e))


@router.get("/rag-collections/verify")
async def verify_rag_collections():
    """
    Verify that all required KB collections exist and are accessible.

    Checks both old agent_ collections and new kb_ collections/aliases.

    Returns:
        JSON response with verification results
    """
    try:
        import os
        from rag.qdrant import get_qdrant_client

        logger.info("Verifying RAG collection migration")

        vector_db = os.getenv("VECTOR_DB", "qdrant").lower()
        if vector_db != "qdrant":
            raise HTTPException(
                status_code=400, detail=f"Verification only supported for Qdrant, current VECTOR_DB={vector_db}"
            )

        # Get mappings
        mappings_response = await get_rag_collection_mappings()
        mappings = mappings_response["mappings"]

        qdrant_client = get_qdrant_client()

        verification = {"old_collections": [], "new_collections": [], "missing_old": [], "missing_new": []}

        for mapping in mappings:
            old_name = mapping["old_collection"]
            new_name = mapping["new_collection"]

            # Check old collection
            try:
                old_coll = qdrant_client.get_collection(old_name)
                verification["old_collections"].append(
                    {"name": old_name, "points": old_coll.points_count, "exists": True}
                )
            except Exception:
                verification["missing_old"].append(old_name)

            # Check new collection (or alias)
            try:
                new_coll = qdrant_client.get_collection(new_name)
                verification["new_collections"].append(
                    {"name": new_name, "points": new_coll.points_count, "exists": True}
                )
            except Exception:
                verification["missing_new"].append(new_name)

        verification["summary"] = {
            "total_mappings": len(mappings),
            "old_collections_found": len(verification["old_collections"]),
            "new_collections_found": len(verification["new_collections"]),
            "old_collections_missing": len(verification["missing_old"]),
            "new_collections_missing": len(verification["missing_new"]),
        }

        verification["migration_complete"] = len(verification["missing_new"]) == 0

        return {"success": True, "verification": verification}

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error verifying RAG collections: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e))


@router.post("/rag-collections/cleanup")
async def cleanup_old_rag_collections(confirm: bool = False):
    """
    Delete old agent_ collections after verifying new kb_ collections work.

    CAUTION: This is destructive! Only run after thorough testing.

    Args:
        confirm: Must be true to actually delete collections

    Returns:
        JSON response with cleanup results
    """
    try:
        import os
        from rag.qdrant import get_qdrant_client

        if not confirm:
            raise HTTPException(status_code=400, detail="Cleanup requires explicit confirmation. Send confirm=true")

        logger.warning("Starting cleanup of old RAG collections")

        vector_db = os.getenv("VECTOR_DB", "qdrant").lower()
        if vector_db != "qdrant":
            raise HTTPException(
                status_code=400, detail=f"Cleanup only supported for Qdrant, current VECTOR_DB={vector_db}"
            )

        # First verify migration is complete
        verification_response = await verify_rag_collections()
        verification = verification_response["verification"]

        if not verification["migration_complete"]:
            raise HTTPException(
                status_code=400,
                detail=f"Migration not complete: {len(verification['missing_new'])} new collections missing",
            )

        # Get mappings
        mappings_response = await get_rag_collection_mappings()
        mappings = mappings_response["mappings"]

        qdrant_client = get_qdrant_client()

        deleted = []
        errors = []

        for mapping in mappings:
            old_name = mapping["old_collection"]

            try:
                # Verify old collection exists
                try:
                    old_coll = qdrant_client.get_collection(old_name)
                    old_count = old_coll.points_count
                except Exception:
                    logger.info(f"Old collection {old_name} not found, skipping")
                    continue

                # Delete old collection
                logger.warning(f"Deleting old collection {old_name} ({old_count} points)")
                qdrant_client.delete_collection(old_name)

                deleted.append({"collection": old_name, "points_deleted": old_count})

                logger.info(f"✓ Deleted {old_name}")

            except Exception as e:
                logger.error(f"Error deleting {old_name}: {e}", exc_info=True)
                errors.append({"collection": old_name, "error": str(e)})

        return {
            "success": len(errors) == 0,
            "deleted_count": len(deleted),
            "error_count": len(errors),
            "deleted": deleted,
            "errors": errors,
            "message": f"Cleanup complete: {len(deleted)} old collections deleted",
        }

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error cleaning up old RAG collections: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=str(e))
