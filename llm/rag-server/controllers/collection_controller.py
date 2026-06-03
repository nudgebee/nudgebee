import json
import logging
from typing import Any, Dict, Optional

from fastapi import APIRouter, HTTPException, Query, Response

from rag.core.documents import collection as document_collection
from rag.exceptions import is_not_found_error

router = APIRouter()
logger = logging.getLogger(__name__)


@router.get("/collections")
async def list_collections(module: Optional[str] = Query(None)):
    """List all collections, optionally filtered by module."""
    data = document_collection.list_collections(module=module)
    return {"data": data}


# Specific routes MUST come before generic route with path parameter
@router.get("/collections/{collection_name}/query")
async def query_collection(
    collection_name: str,
    query: str = Query(""),
    limit: int = Query(10),
):
    """Query a specific collection."""
    try:
        result = document_collection.query_collection(collection_name, query, limit)
        return {"data": result}
    except Exception as e:
        if is_not_found_error(e):
            raise HTTPException(status_code=404, detail=f"Error downloading collection '{collection_name}': {str(e)}")
        raise


@router.get("/collections/{collection_name}/download")
async def download_collection(collection_name: str):
    """Download a collection as JSON file."""
    try:
        collection = document_collection.get_collection(collection_name)
        if collection is None:
            raise HTTPException(status_code=404, detail=f"Collection '{collection_name}' not found")

        # Return JSON response with download headers
        content = json.dumps(collection["data"], indent=2)
        return Response(
            content=content,
            media_type="application/json",
            headers={"Content-Disposition": f"attachment; filename={collection_name}.json"},
        )
    except Exception as e:
        if is_not_found_error(e):
            logger.warning(f"Collection Not found {collection_name}: {e}")
            raise HTTPException(status_code=404, detail=f"Collection Not found - '{collection_name}': {str(e)}")
        logger.error(f"Error downloading collection {collection_name}: {e}")
        raise HTTPException(status_code=500, detail=f"Error downloading collection '{collection_name}': {str(e)}")


# Generic route - MUST come after specific routes
@router.get("/collections/{collection_name}")
async def get_collection(collection_name: str):
    """Get details of a specific collection."""
    collection = document_collection.get_collection(collection_name)
    if collection is None:
        raise HTTPException(status_code=404, detail=f"Collection '{collection_name}' not found")
    return {"data": collection}


@router.delete("/collections")
async def delete_all_collections():
    """Delete all collections."""
    document_collection.delete_all_collections()
    return {"data": "ok"}


@router.delete("/collections/{collection_name}")
async def delete_collection(collection_name: str):
    """Delete a specific collection."""
    document_collection.delete_collection(collection_name)
    return {"data": "ok"}


@router.delete("/collections/module/{module_name}")
async def delete_module_collections(module_name: str):
    """Delete all collections for a specific module."""
    document_collection.delete_module_collections(module_name)
    return {"data": "ok"}


@router.post("/admin/configure-ondisk")
async def configure_ondisk_storage():
    """
    Configure all collections to use on-disk storage.

    This endpoint:
    - Checks all collections for on-disk storage configuration
    - Configures collections that don't have it enabled
    - Returns detailed status of each collection

    On-disk storage significantly reduces memory usage by storing vectors on disk
    instead of in RAM.

    Returns:
        {
            "total_collections": int,
            "configured": int,
            "already_configured": int,
            "newly_configured": int,
            "failed": int,
            "details": [
                {
                    "collection": str,
                    "status": "already_configured" | "newly_configured" | "failed",
                    "on_disk": bool,
                    "error": str (only if failed)
                }
            ]
        }
    """
    try:
        from rag.qdrant.client import get_qdrant_client, _configure_ondisk_storage

        client = get_qdrant_client()
        result = _configure_ondisk_storage(client)

        return {"data": result}
    except Exception as e:
        logger.error(f"Error configuring on-disk storage: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=f"Error configuring on-disk storage: {str(e)}")


@router.post("/admin/export-qdrant-collections")
async def export_qdrant_collections():
    """
    Export all collections from Qdrant.

    This endpoint exports all collections with their complete data
    (vectors, documents, metadata) for migration to another Qdrant instance.

    Use case: Migrating from embedded Qdrant to standalone Qdrant server.

    Returns:
        {
            "total_collections": int,
            "exported_collections": int,
            "failed_collections": int,
            "collections": [
                {
                    "name": str,
                    "metadata": dict,
                    "ids": list,
                    "documents": list,
                    "embeddings": list,
                    "metadatas": list
                }
            ],
            "errors": list
        }
    """
    try:
        from rag.migration.qdrant_exporter import QdrantExporter

        logger.info("Starting Qdrant collections export")
        exporter = QdrantExporter()
        result = exporter.export_all_collections(batch_size=100)

        if result["failed_collections"] > 0:
            logger.warning(f"Export completed with {result['failed_collections']} failures")

        logger.info(
            f"Export complete: {result['exported_collections']}/{result['total_collections']} collections exported"
        )

        return {"data": result}

    except Exception as e:
        logger.error(f"Error exporting Qdrant collections: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=f"Error exporting Qdrant collections: {str(e)}")


@router.post("/admin/import-qdrant-collections")
async def import_qdrant_collections(import_data: dict):
    """
    Import collections into Qdrant.

    This endpoint imports collections from export data into Qdrant.
    Collections are created with on_disk=True for both vectors and HNSW index.

    Use case: Completing migration from embedded Qdrant to standalone Qdrant server.

    Request body: Export data from /admin/export-qdrant-collections endpoint

    Returns:
        {
            "total_collections": int,
            "imported_collections": int,
            "failed_collections": int,
            "details": [
                {
                    "collection": str,
                    "status": "success" | "failed",
                    "points_imported": int,
                    "error": str (only if failed)
                }
            ]
        }
    """
    try:
        from rag.migration.qdrant_importer import QdrantImporter

        logger.info("Starting Qdrant collections import")

        if "collections" not in import_data:
            raise HTTPException(status_code=400, detail="Invalid import data: missing 'collections' field")

        importer = QdrantImporter()
        collections = import_data["collections"]

        result: Dict[str, Any] = {
            "total_collections": len(collections),
            "imported_collections": 0,
            "failed_collections": 0,
            "details": [],
        }

        for collection_data in collections:
            collection_name = collection_data.get("name")
            if not collection_name:
                logger.warning("Skipping collection without name")
                result["failed_collections"] += 1
                result["details"].append(
                    {"collection": "unknown", "status": "failed", "error": "Missing collection name"}
                )
                continue

            try:
                logger.info(f"Importing collection: {collection_name}")
                success = importer.import_collection(collection_data, batch_size=100)

                if success:
                    points_count = len(collection_data.get("ids", []))
                    result["imported_collections"] += 1
                    result["details"].append(
                        {"collection": collection_name, "status": "success", "points_imported": points_count}
                    )
                    logger.info(f"Successfully imported collection: {collection_name}")
                else:
                    result["failed_collections"] += 1
                    result["details"].append(
                        {"collection": collection_name, "status": "failed", "error": "Import returned false"}
                    )

            except Exception as e:
                logger.error(f"Error importing collection {collection_name}: {e}", exc_info=True)
                result["failed_collections"] += 1
                result["details"].append({"collection": collection_name, "status": "failed", "error": str(e)})

        logger.info(
            f"Import complete: {result['imported_collections']}/{result['total_collections']} collections imported"
        )

        return {"data": result}

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error importing Qdrant collections: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=f"Error importing Qdrant collections: {str(e)}")
