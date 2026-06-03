"""
Cache management controller for RAG server.

Provides endpoints for managing the collection index cache.
"""

import logging

from fastapi import APIRouter, HTTPException

from rag.core.cache import get_collection_list_cache

router = APIRouter()
logger = logging.getLogger(__name__)


@router.get("/cache/stats")
async def get_cache_stats():
    """
    Get statistics for collection list cache.

    Returns cache performance metrics including:
    - Total collections cached
    - Cache age and freshness
    - TTL settings
    - Whether cache is populated
    """
    try:
        cache = get_collection_list_cache()
        stats = {"collection_list": cache.stats()}

        logger.info(f"Cache stats requested: {stats}")
        return {"data": stats}

    except Exception as e:
        logger.error(f"Error getting cache stats: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=f"Error getting cache stats: {str(e)}")


@router.post("/cache/clear")
async def clear_cache():
    """
    Clear the collection list cache.

    Use this when:
    - Collections are created/updated/deleted
    - Need to force cache refresh
    - Testing cache behavior
    """
    try:
        cache = get_collection_list_cache()

        # Get stats before clearing
        before_stats = cache.stats()

        # Clear cache
        cache.clear()

        logger.info("Collection list cache cleared successfully")

        return {"data": {"status": "cleared", "before": before_stats}}

    except Exception as e:
        logger.error(f"Error clearing cache: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=f"Error clearing cache: {str(e)}")
