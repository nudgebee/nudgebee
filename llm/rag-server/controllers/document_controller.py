"""
Document loading controller.

Handles document ingestion endpoints for modules and account-specific documents.
"""

import gc
import logging
import os
import tempfile
import threading
import time
from typing import List, Optional, Union, Any

import aiofiles
from fastapi import APIRouter, HTTPException
from filelock import FileLock, Timeout
from rag.core.documents.loaders.file_loaders import (
    CSVLoader,
    JSONLoader,
    TextLoader,
    UnstructuredXMLLoader,
)
from pydantic import BaseModel

from rag.core.documents import collection as document_collection
from rag.core.documents.loaders import (
    load_account_module_docs,
    load_events_json_docs,
    load_loki_docs,
    load_planner_docs,
    load_prom_json_docs,
    load_recommendations_json_docs,
)
from rag.core.documents.scraper import load_confluence_docs, load_nudgebee_docs, load_servicenow_kb
from utils.config import Module
from utils.shared import release_lock

router = APIRouter()
logger = logging.getLogger(__name__)

# Create a locks directory if it doesn't exist
lock_dir = "./.locks"
os.makedirs(lock_dir, exist_ok=True)

# Modules whose payload represents a single logical document and must NOT be
# split per-line at ingest. See `load_account_module_docs_api` for context.
_NO_LINE_SPLIT_MODULES = {"long_term_memory"}


# Pydantic models
class LoadDocsRequest(BaseModel):
    module: Optional[str] = None
    force: bool = False
    account_id: Optional[List[str]] = None


class LoadAccountModuleDocsRequest(BaseModel):
    module: str
    account_id: str
    data: str
    format: str = "text"
    id: Optional[str] = None
    # Caller-supplied tags stamped onto every loaded document's metadata
    # (e.g. ``{"_id": "<uuid>", "memory_type": "investigation_result"}``).
    # Pre-fix this field was absent from the model, so Pydantic silently
    # discarded it — leaving documents with only LineByLineLoader-added
    # keys (``source``, ``line_number``).
    metadata: Optional[dict] = None


class DeleteAccountModuleDocsRequest(BaseModel):
    module: str
    account_id: str
    id: str


def _create_document_loader(
    file_path: str, data_format: str
) -> Union[JSONLoader, UnstructuredXMLLoader, CSVLoader, TextLoader]:
    """
    Create appropriate document loader based on data format.

    For text format, each newline will be treated as a separate document
    via post-processing in _load_and_split_documents().
    """
    logger.info(f"Loading documents from temporary file with format: {data_format}")
    if data_format == "json":
        return JSONLoader(file_path, jq_schema=".[]", text_content=False)
    elif data_format == "xml":
        return UnstructuredXMLLoader(file_path)
    elif data_format == "csv":
        return CSVLoader(file_path, csv_args={"delimiter": ","})
    else:
        # Text format - will be split line-by-line in _load_and_split_documents()
        return TextLoader(file_path)


class LineByLineLoader:
    """
    Wrapper loader that splits text documents line-by-line.

    Each non-empty line becomes a separate document, improving RAG precision
    by creating individual searchable chunks.
    """

    def __init__(self, base_loader, data_format: str):
        self.base_loader = base_loader
        self.data_format = data_format

    def load(self):
        """Load documents and split by lines for text format."""
        from rag.core.types import Document

        documents = self.base_loader.load()

        # For text format, split each line into a separate document
        if self.data_format == "text":
            split_docs = []
            for doc in documents:
                lines = doc.page_content.split("\n")
                for i, line in enumerate(lines):
                    line = line.strip()
                    if line:  # Skip empty lines
                        # Create new document for each line, preserving metadata
                        new_doc = Document(
                            page_content=line,
                            metadata={
                                **doc.metadata,
                                "line_number": i + 1,
                                "source_line": True,
                            },
                        )
                        split_docs.append(new_doc)
            logger.info(f"Split {len(documents)} text document(s) into {len(split_docs)} line-based documents")
            return split_docs

        # For other formats (json, xml, csv), return as-is
        return documents


def _cleanup_temp_file(file_path: str) -> None:
    """Clean up temporary file if it exists."""
    if os.path.exists(file_path):
        try:
            os.unlink(file_path)
        except Exception as ex:
            logger.error(f"Failed to delete temporary file {file_path}: {ex}")


def _load_all_docs(account_ids: Optional[List[str]], force: bool = False) -> None:
    """Load all documents with memory optimization and error handling."""
    module_loaders: List[tuple[str, Any]] = [
        ("prometheus", load_prom_json_docs),
        ("loki", load_loki_docs),
        ("planner", load_planner_docs),
        ("confluence", load_confluence_docs),
        ("servicenow", load_servicenow_kb),
        ("events", load_events_json_docs),
        ("recommendations", load_recommendations_json_docs),
        ("nudgebee_docs", load_nudgebee_docs),
    ]

    for module_name, loader_func in module_loaders:
        try:
            logger.info(f"Loading {module_name} documents...")
            loader_func(account_ids, force)
            logger.info(f"Completed loading {module_name} documents")

        except Exception as e:
            logger.error(f"Error loading {module_name} documents: {e}")
            # Continue with next module even if one fails
            continue
        finally:
            # Clear embeddings instance cache between modules to release
            # genai/httpx client memory accumulated during bulk processing.
            # Guard so an import or clear failure here does NOT replace the
            # original exception from the try block.
            try:
                from rag.core.embeddings.generator import (
                    _embeddings_instance_cache,
                    _instance_cache_lock,
                )

                with _instance_cache_lock:
                    _embeddings_instance_cache.clear()
            except Exception:
                logger.warning("Failed to clear embeddings instance cache", exc_info=True)

            # Force garbage collection after each module to free memory
            gc.collect()


def load_module_docs(module: Optional[str], account_ids: Optional[List[str]], force: bool = False) -> dict:
    """Load module documents - called by API endpoint and startup task."""
    start_time = time.time()
    try:
        # Define module-to-function mapping
        module_loaders = {
            Module.prometheus.value: load_prom_json_docs,
            Module.loki.value: load_loki_docs,
            Module.recommendations.value: load_recommendations_json_docs,
            Module.events.value: load_events_json_docs,
            Module.planner.value: load_planner_docs,
            Module.nudgebee_docs.value: load_nudgebee_docs,
        }

        # Special case for docs which loads multiple document types.
        # Confluence + ServiceNow + Nudgebee product docs all now land in the
        # unified ``knowledge_base`` module namespace (PR #28733), so a call
        # with module="knowledge_base" (or the legacy alias "docs") must
        # refresh all three sources — otherwise the unified search would
        # return stale results for whichever source wasn't re-ingested.
        if module == Module.docs.value or module == Module.kb.value:
            load_confluence_docs(account_ids, force)
            load_servicenow_kb(account_ids, force)
            load_nudgebee_docs(account_ids, force)
        # Use the mapping if module is found
        elif module in module_loaders:
            module_loaders[module](account_ids, force)
        # Default case - load all docs
        else:
            logger.info(f"Loading all documents as module '{module}' is not recognized.")
            _load_all_docs(account_ids, force)

        return {"status": "ok"}
    except Exception as e:
        logger.exception(f"Error loading documents: {e}")
        return {"status": "error", "error": str(e)}
    finally:
        # Force garbage collection to ensure memory is freed
        gc.collect()
        logger.info(f"Documents loaded in {time.time() - start_time} sec")


@router.post("/load_docs")
async def load_docs_api(request: LoadDocsRequest):
    """Load documents for a specific module or all modules."""
    account_ids = request.account_id
    module = request.module
    force = request.force

    if account_ids is not None and not isinstance(account_ids, list):
        raise HTTPException(status_code=400, detail="account_id should be a list")

    # Use a special identifier for "all modules" case
    module_key = module if module else "all_modules"
    lock_file = f"{lock_dir}/module_{module_key}.lock"

    # Create a lock but don't acquire it yet
    lock = FileLock(lock_file, timeout=0)
    module_msg = "module " + module_key if module else "all modules"
    try:
        # Try to acquire lock (non-blocking)
        lock.acquire(blocking=False)
    except Timeout:
        logger.info(f"Generating embeddings is already running for {module_msg}")
        return {"status": f"Generating embeddings is already running for {module_msg}. Please wait until it finishes."}

    # Store the lock file path for potential cleanup
    lock_path = lock.lock_file

    def load_and_release():
        try:
            logger.info(f"Generating embeddings for {module_msg}")
            try:
                load_module_docs(module, account_ids, force)
            except Exception as e:
                logger.exception(f"Error generating embeddings module docs: {e}")
            finally:
                logger.info(f"Finished generating embeddings for {module_msg}")
                release_lock(lock, lock_path, module_key)
        except Exception as thread_error:
            logger.exception(f"Unexpected thread error: {thread_error}")

    # Start the thread and return immediately
    threading.Thread(target=load_and_release, daemon=True).start()
    return {"status": "Generating embeddings..."}


@router.post("/load_account_module_docs")
async def load_account_module_docs_api(request: LoadAccountModuleDocsRequest):
    """Load documents for a specific account and module."""
    module = request.module
    account_id = request.account_id
    rag_data = request.data
    rag_data_format = request.format
    doc_id = request.id
    extra_metadata = request.metadata

    # Create a unique key for this account+module combination
    account_module_key = f"account_{account_id}_module_{module}"
    if doc_id:
        account_module_key += f"_doc_{doc_id}"

    lock_file = f"{lock_dir}/{account_module_key}.lock"

    # Try to acquire lock (non-blocking)
    lock = FileLock(lock_file, timeout=0)
    try:
        lock.acquire(blocking=False)
    except Timeout:
        logger.info(f"Module '{module}' for account '{account_id}' is already loading")
        return {"status": f"Module '{module}' for account '{account_id}' is already loading"}

    # Store the lock file path for potential cleanup
    lock_path = lock.lock_file

    # Create temp file path (will write async)
    tmp_fd, tmp_path = tempfile.mkstemp(prefix="nb_rag_", suffix=".data")
    os.close(tmp_fd)  # Close file descriptor, we'll use aiofiles

    def load_and_release(data_loader, tmp_path, doc_id):
        try:
            logger.info(f"Loading documents for account: {account_id}, module: {module}")
            load_account_module_docs(account_id, module, data_loader, doc_id=doc_id, extra_metadata=extra_metadata)
        finally:
            logger.info(f"Finished loading documents for account: {account_id}, module: {module}")
            release_lock(lock, lock_path, account_module_key)
            _cleanup_temp_file(tmp_path)

    try:
        logger.info(f"Storing {module} data in temporary file for account_id: {account_id}, file name: {tmp_path}")
        # Use async file write
        async with aiofiles.open(tmp_path, mode="w") as tmp:
            await tmp.write(rag_data)
        logger.info(f"Temporary file created: {tmp_path}")

        try:
            base_loader = _create_document_loader(tmp_path, rag_data_format)
            # Memory entries are a single logical unit — splitting on newlines
            # fragments them into separate Qdrant points where only the first
            # carries the caller-supplied ``id``; the rest end up with
            # content-hash UUIDs (md5 of the line) that don't map back to any
            # ``llm_conversation_memory`` row. That's the source of the phantom
            # results the llm-server parser can't anchor. Other modules
            # (KB-style dumps, agent rag) still benefit from per-line splits.
            #
            # ``loader`` is annotated ``Any`` because the two branches return
            # different concrete types (file loader vs. wrapping
            # ``LineByLineLoader``) and the downstream consumer only relies on
            # the structural ``.load()`` contract.
            loader: Any
            if module in _NO_LINE_SPLIT_MODULES:
                loader = base_loader
            else:
                loader = LineByLineLoader(base_loader, rag_data_format)
        except Exception as e:
            logger.error(f"Failed to load data from temporary file: {e}")
            release_lock(lock, lock_path, account_module_key)
            raise HTTPException(status_code=400, detail=str(e))

        threading.Thread(target=load_and_release, args=(loader, tmp_path, doc_id), daemon=True).start()
        return {"status": "loading"}

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Failed to prepare data: {e}")
        release_lock(lock, lock_path, account_module_key)
        _cleanup_temp_file(tmp_path)
        raise HTTPException(status_code=400, detail=str(e))


@router.post("/delete_account_module_docs")
async def delete_account_module_docs_api(request: DeleteAccountModuleDocsRequest):
    """Delete a specific document for an account and module."""
    module = request.module
    account_id = request.account_id
    doc_id = request.id

    from utils.shared import get_collection_name

    collection_name = get_collection_name(account_id, module)

    if not collection_name:
        logger.error(f"Could not determine collection name for account {account_id} and module {module}")
        raise HTTPException(status_code=400, detail="Could not determine collection name")

    logger.info(f"Deleting document {doc_id} from collection {collection_name}")
    try:
        success = document_collection.delete_document_by_id(collection_name, doc_id)
        if success:
            return {"status": "ok"}
        else:
            raise HTTPException(status_code=500, detail="Failed to delete document")
    except Exception as e:
        logger.error(f"Error deleting document: {e}")
        raise HTTPException(status_code=500, detail=str(e))


@router.delete("/delete_collections")
async def delete_collections():
    """Delete all collections."""
    logger.info("Deleting collections")
    try:
        document_collection.delete_all_collections()
    except Exception as e:
        logger.error(f"Error deleting collections: {e}")
        return {"status": "error", "error": str(e)}
    return {"status": "ok"}
