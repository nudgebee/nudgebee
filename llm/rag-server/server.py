# added a test change which does absolutely nothing

import logging
import os
import threading
from contextlib import asynccontextmanager

import httpx
from dotenv import load_dotenv
from fastapi import FastAPI
from filelock import FileLock, Timeout
from qdrant_client.http.exceptions import ResponseHandlingException

from config import setup_logger
from controllers.document_controller import load_module_docs
from rag.core.documents import collection as document_collection
from rag.core.documents import module_retag_migration
from utils.shared import set_global_trace, release_lock

os.environ["TF_ENABLE_ONEDNN_OPTS"] = "0"

RAG_SERVER_PORT = int(os.environ.get("RAGSERVER_PORT", 9999))

# Setup logging and environment
load_dotenv()
setup_logger()
logger = logging.getLogger(__name__)

lock_dir = "./.locks"


def post_startup_task():
    """Background task to load documents on startup if needed."""
    logger.info("Post startup task running - loading documents")

    # Create locks directory if it doesn't exist
    os.makedirs(lock_dir, exist_ok=True)

    # Create a lock file for startup document loading
    lock_file = f"{lock_dir}/startup_load_docs.lock"
    lock = FileLock(lock_file, timeout=0)
    # Store the lock file path for potential cleanup
    lock_path = lock.lock_file

    try:
        # Try to acquire lock (non-blocking)
        lock.acquire(blocking=False)
        logger.info("Acquired lock for startup document loading")

        try:
            # Warm collection list cache asynchronously to avoid blocking startup
            def warm_cache_async():
                try:
                    logger.info("Warming collection list cache (async)...")
                    from rag.qdrant.client import list_collections_optimized

                    collections = list_collections_optimized()
                    logger.info(f"Cache warmed with {len(collections)} collections")
                except Exception as e:
                    logger.warning(f"Cache warming failed (non-critical): {e}")

            threading.Thread(target=warm_cache_async, daemon=True, name="cache-warmer").start()
            logger.info("Started background cache warming thread")

            logger.info("Checking if collections exist...")
            has_collections = document_collection.has_collections()
            logger.info(f"Collection existence check completed - exists: {has_collections}")

            # Retag any legacy ``docs`` / ``nudgebee_docs`` collections to
            # ``knowledge_base`` BEFORE deciding to load docs. Running it first
            # means: (a) if the retag renames collections and leaves none under
            # the old names, the has_collections check above is already accurate;
            # (b) an empty Qdrant still short-circuits to document loading below
            # without doing any work. Gated behind an env flag so it can be
            # disabled in case of regressions.
            if module_retag_migration.is_enabled() and has_collections:
                try:
                    retag_summary = module_retag_migration.retag_legacy_modules()
                    logger.info(f"Module retag migration summary: {retag_summary}")
                except Exception as retag_exc:  # defensive: migration must never block startup
                    logger.exception(f"Module retag migration failed (non-fatal): {retag_exc}")

            if not has_collections:
                logger.info("No collections found - starting document loading...")
                # Load docs with memory optimization
                result = load_module_docs(module=None, account_ids=None)
                logger.info(f"Document loading result: {result}")
            else:
                logger.info("Found existing collections - skipping document loading")
        except (
            ConnectionError,
            ResponseHandlingException,
            httpx.RequestError,
            httpx.TimeoutException,
            httpx.ConnectError,
        ) as e:
            logger.warning(f"Cannot connect to Qdrant during startup - skipping collection loading: {e}")
            logger.info("Collections will need to be loaded manually when Qdrant becomes available")
        except Exception as e:
            logger.exception(f"Error in startup document loading: {e}")
        finally:
            logger.info("Finished loading documents at startup")
            release_lock(lock, lock_path, "startup_load_docs")
    except Timeout:
        # Another worker is already handling the document loading
        logger.info("Startup document loading is already in progress by another worker")
    except Exception as e:
        logger.warning(f"Unexpected error in post_startup_task: {e}")


@asynccontextmanager
async def lifespan(app: FastAPI):
    """
    Lifespan context manager for FastAPI startup and shutdown events.
    Replaces @app.on_event("startup") and @app.on_event("shutdown").
    """
    # Startup
    set_global_trace()
    logger.info("FastAPI application starting up...")

    # Start the post-startup task in background thread
    thread = threading.Thread(target=post_startup_task, daemon=True)
    thread.start()

    yield

    # Shutdown
    logger.info("FastAPI application shutting down...")


def create_app() -> FastAPI:
    """Create and configure the FastAPI application."""
    app = FastAPI(
        title="RAG Server",
        description="Retrieval Augmented Generation Server with Qdrant",
        version="1.0.0",
        lifespan=lifespan,
    )

    # Register routers from controllers
    from controllers import (
        health,
        collection_controller,
        migration_controller,
        knowledgebase_controller,
        document_controller,
        search_controller,
        cache_controller,
    )

    app.include_router(health.router, tags=["health"])
    app.include_router(collection_controller.router, tags=["collections"])
    app.include_router(migration_controller.router, tags=["migration"])
    app.include_router(knowledgebase_controller.router, prefix="/api/v1", tags=["knowledgebase"])
    app.include_router(document_controller.router, tags=["documents"])
    app.include_router(search_controller.router, tags=["search"])
    app.include_router(cache_controller.router, tags=["cache"])

    return app


# Create application instance
app = create_app()

if __name__ == "__main__":
    # This block is for running the server directly with `python server.py` for local debugging
    import uvicorn

    logger.info("Starting Uvicorn server for debugging...")

    # Use the same logging config for uvicorn that setup_logger() loaded
    log_config_file = os.getenv("LOGGING_CONFIG_FILE")
    if not log_config_file:
        log_config_file = os.path.join(os.path.dirname(__file__), "config", "logging.json")

    logger.info(f"Using logging config: {log_config_file}")

    uvicorn.run(
        "server:app",
        host="0.0.0.0",
        port=RAG_SERVER_PORT,
        reload=False,
        workers=1,  # Single worker for Qdrant persistent mode
        log_config=log_config_file,  # Use same config as application
    )
