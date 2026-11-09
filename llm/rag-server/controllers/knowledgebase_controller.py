import asyncio
import logging
import os
import tempfile
import threading
from typing import Optional, Protocol

import aiofiles
import aiofiles.os
from fastapi import APIRouter, HTTPException
from rag.core.documents.loaders.file_loaders import (
    CSVLoader,
    JSONLoader,
    TextLoader,
    UnstructuredXMLLoader,
)
from pydantic import BaseModel, Field

from rag.core.documents import collection as document_collection
from rag.core.documents.processing import process_documents
from rag.core.embeddings.generator import get_embeddings
from rag.core.llm.rag import get_matching_documents
from rag.core.types import Document
from rag.core.documents.knowledge_tags import add_knowledge_context_tags
from rag.core.utils.db_query import get_first_active_account_for_tenant
from rag.core.documents import figure_cache, pdf_ingest, source_store

router = APIRouter()
logger = logging.getLogger(__name__)


# Pydantic models
class KBCreateRequest(BaseModel):
    # An organisation knowledge base has no account: it sends tenant_id instead,
    # and its collection is scoped to the tenant.
    account_id: Optional[str] = None
    tenant_id: Optional[str] = None
    kb_id: str
    data: str
    format: str = "text"
    # Only a PDF needs it: it heads every chunk, so a retrieved passage says
    # which document it came from.
    filename: str = ""
    triggered_by: str = "system"
    trigger_type: str = "user_create"
    context_tags: list[str] = Field(default_factory=list)


class KBScopeRequest(BaseModel):
    kb_id: str
    # Exactly one: account_id for an account knowledge base, tenant_id for an
    # organisation one.
    account_id: Optional[str] = None
    tenant_id: Optional[str] = None


def _config_account_id(account_id: Optional[str], tenant_id: Optional[str]) -> str:
    """Account whose LLM settings embed (and describe the figures of) a knowledge base.

    An organisation knowledge base has no account; any active account in the
    tenant serves, as for the tenant's integration knowledge bases.
    """
    if account_id:
        return account_id
    # "" falls back to the server's own LLM settings, as get_embeddings does for no account.
    if not tenant_id:
        return ""
    account = get_first_active_account_for_tenant(tenant_id)
    if not account:
        logger.warning("No active account in tenant %s; using the server's default LLM settings", tenant_id)
    return str(account or "")


class KBSearchRequest(BaseModel):
    account_id: str
    kb_id: str
    query: str
    k: int = 3
    conversation_id: Optional[str] = None
    message_id: Optional[str] = None
    user_id: Optional[str] = None
    agent_id: Optional[str] = None
    track_token_usage: bool = True


class KBRetriggerIntegrationRequest(BaseModel):
    # Unused: the scrape is selected by integration_id. Empty for an
    # integration's organisation knowledge base (#40605).
    account_id: str = ""
    integration_id: str
    kb_source: str
    triggered_by: str = "system"


class DocumentLoader(Protocol):
    def load(self) -> list[Document]:
        raise NotImplementedError


def _create_kb_document_loader(file_path: str, data_format: str) -> DocumentLoader:
    """
    Create appropriate document loader based on data format.
    """
    logger.info(f"Loading KB documents from temporary file with format: {data_format}")
    if data_format == "json":
        return JSONLoader(file_path, jq_schema=".[]", text_content=False)
    elif data_format == "xml":
        return UnstructuredXMLLoader(file_path)
    elif data_format == "csv":
        return CSVLoader(file_path, csv_args={"delimiter": ","})
    elif data_format == "text":
        return TextLoader(file_path)
    else:
        # Default to text format. Note "pdf" never reaches here: create_kb routes a PDF to
        # _create_pdf_kb, which reads the document and describes its figures before there
        # is anything to index.
        return TextLoader(file_path)


class KBLineByLineLoader:
    """
    Wrapper loader that splits text documents line-by-line for KB.
    Each non-empty line becomes a separate document for semantic search.
    """

    def __init__(self, base_loader: DocumentLoader, data_format: str) -> None:
        self.base_loader = base_loader
        self.data_format = data_format

    def load(self) -> list[Document]:
        """Load documents and split by lines for text format."""
        documents = self.base_loader.load()

        # For text format, split each line into a separate document
        if self.data_format == "text":
            split_docs = []
            for doc in documents:
                lines = doc.page_content.split("\n")
                for i, line in enumerate(lines):
                    line = line.strip()
                    if line:  # Skip empty lines
                        new_doc = Document(
                            page_content=line,
                            metadata={
                                **doc.metadata,
                                "line_number": i + 1,
                                "source_line": True,
                            },
                        )
                        split_docs.append(new_doc)
            return split_docs
        else:
            # For JSON, CSV, XML - return as-is
            return documents


async def _create_pdf_kb(request: KBCreateRequest, collection_name: str, config_account_id: str) -> dict:
    """Index a PDF: page text plus a description of every figure.

    Kept apart from the text path because a PDF arrives as base64 and its
    figures have to be read by a vision model before there is anything to
    index — none of which survives being written to a text temp file.
    """
    try:
        documents, warning = await asyncio.to_thread(
            pdf_ingest.build_pdf_documents,
            request.data,
            config_account_id,
            request.filename,
            None,
            collection_name,
        )
    except ValueError as exc:
        # Corrupt, password-protected, not a PDF, or nothing readable in it: all of them
        # are the uploader's to fix, and all of them carry a reason worth showing.
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    documents = add_knowledge_context_tags(documents, request.context_tags)
    embeddings = await asyncio.to_thread(get_embeddings, config_account_id)

    _, document_ids = await asyncio.to_thread(
        process_documents,
        documents=documents,
        embeddings=embeddings,
        account_id=request.account_id,
        tenant_id=None if request.account_id else request.tenant_id,
        classify_account_id=config_account_id,
        module="knowledge_base",
        collection_name=collection_name,
        knowledgebase_id=request.kb_id,
        triggered_by=request.triggered_by,
        trigger_type=request.trigger_type,
        source="user_kb",
        warning=warning,
        # One uploaded file is one document. build_pdf_documents has already broken it
        # into passages for retrieval, and counting those reported a six-page PDF as
        # eight documents — a number matching neither the file nor its pages.
        source_document_count=1,
    )
    if not document_ids:
        # process_documents swallows embedding and storage failures and returns no ids.
        # Reporting the chunks we built as the document count would turn a load that
        # indexed nothing into a successful one — the silent partial index this whole
        # path exists to prevent.
        raise HTTPException(status_code=500, detail="The PDF was read, but none of its content could be indexed")

    logger.info("Created PDF KB collection %s with %d documents", collection_name, len(document_ids))
    return {
        "status": "success",
        "collection": collection_name,
        "document_count": 1,
        "warning": warning,
    }


@router.post("/kb/create")
async def create_kb(request: KBCreateRequest):
    """
    Create a knowledgebase vector collection with embeddings.

    Collection name format: kb_{account_id}_{kb_id}
    Supports formats: json, xml, csv, text, pdf
    """
    if not request.account_id and not request.tenant_id:
        raise HTTPException(status_code=400, detail="account_id or tenant_id is required")
    try:
        logger.info(
            f"Creating KB collection for account_id={request.account_id}, kb_id={request.kb_id}, "
            f"format={request.format}"
        )

        # Generate collection name
        collection_name = f"kb_{request.kb_id}"
        config_account_id = await asyncio.to_thread(_config_account_id, request.account_id, request.tenant_id)

        if request.format == "pdf":
            return await _create_pdf_kb(request, collection_name, config_account_id)

        # Write data to temporary file asynchronously
        tmp_fd, tmp_path = await asyncio.to_thread(tempfile.mkstemp, suffix=f".{request.format}", text=True)
        try:
            # Close the file descriptor and write data asynchronously
            os.close(tmp_fd)
            async with aiofiles.open(tmp_path, mode="w") as tmp_file:
                await tmp_file.write(request.data)

            # Create document loader (run in thread as it's I/O heavy)
            base_loader = await asyncio.to_thread(_create_kb_document_loader, tmp_path, request.format)

            # Wrap with line-by-line loader for text chunking
            loader = KBLineByLineLoader(base_loader, request.format)

            # Load documents (blocking I/O - run in thread pool)
            documents = await asyncio.to_thread(loader.load)
            documents = add_knowledge_context_tags(documents, request.context_tags)
            logger.info(f"Loaded {len(documents)} documents for KB {request.kb_id}")

            # A typed note is one document. _KBLoader splits it a line at a time so each
            # line is its own retrieval unit, which is a good way to search it and a
            # misleading way to count it. The other formats are left alone: a JSON or CSV
            # knowledge base really is many records.
            source_document_count = 1 if request.format == "text" else None

            # Get embeddings for the account (run in thread as it may do I/O)
            embeddings = await asyncio.to_thread(get_embeddings, config_account_id)

            # Process documents and create vector collection (blocking - run in thread)
            await asyncio.to_thread(
                process_documents,
                documents=documents,
                embeddings=embeddings,
                account_id=request.account_id,
                tenant_id=None if request.account_id else request.tenant_id,
                classify_account_id=config_account_id,
                module="knowledge_base",
                collection_name=collection_name,
                knowledgebase_id=request.kb_id,
                triggered_by=request.triggered_by,
                trigger_type=request.trigger_type,
                source="user_kb",
                source_document_count=source_document_count,
            )

            recorded = source_document_count if source_document_count is not None else len(documents)
            logger.info(f"Successfully created KB collection: {collection_name} with {recorded} documents")

            return {
                "status": "success",
                "collection": collection_name,
                "document_count": recorded,
            }

        finally:
            # Clean up temporary file asynchronously
            if await aiofiles.os.path.exists(tmp_path):
                await aiofiles.os.remove(tmp_path)

    except Exception as e:
        logger.exception(f"Error creating KB collection: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to create KB collection: {str(e)}")


@router.delete("/kb/{account_id}/{kb_id}")
async def delete_kb(account_id: str, kb_id: str):
    """
    Delete a knowledgebase vector collection.

    Collection name format: kb_{account_id}_{kb_id}
    """
    collection_name = f"kb_{kb_id}"

    try:
        logger.info(f"Deleting KB collection for account_id={account_id}, kb_id={kb_id}")

        # Delete the collection (blocking - run in thread pool)
        await asyncio.to_thread(document_collection.delete_collection, collection_name)
        # Derived stores go with it: source kept for the "view as indexed" screen, and the
        # figure descriptions cached against this knowledge base. Nothing derived from a
        # deleted knowledge base should outlive it.
        await asyncio.to_thread(source_store.drop, collection_name)
        await asyncio.to_thread(figure_cache.forget_collection, collection_name)

        logger.info(f"Successfully deleted KB collection: {collection_name}")

        return {"status": "deleted", "collection": collection_name}

    except Exception as e:
        logger.exception(f"Error deleting KB collection: {e}")
        # Don't raise error if collection doesn't exist
        if "not found" in str(e).lower() or "does not exist" in str(e).lower():
            logger.warning(f"Collection {collection_name} not found, considering it deleted")
            return {"status": "deleted", "collection": collection_name, "note": "collection not found"}
        raise HTTPException(status_code=500, detail=f"Failed to delete KB collection: {str(e)}")


@router.post("/kb/scope")
async def set_kb_scope(request: KBScopeRequest):
    """Move a knowledge base between account and organisation scope without re-indexing it."""
    if bool(request.account_id) == bool(request.tenant_id):
        raise HTTPException(status_code=400, detail="exactly one of account_id or tenant_id is required")
    collection_name = f"kb_{request.kb_id}"
    try:
        updated = await asyncio.to_thread(
            document_collection.set_collection_scope, collection_name, request.account_id, request.tenant_id
        )
    except Exception as e:
        logger.exception(f"Error re-scoping KB collection {collection_name}: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to re-scope KB collection: {str(e)}")
    # updated is False when the knowledge base has no collection yet; that is not an error.
    return {"status": "success", "collection": collection_name, "updated": updated}


@router.post("/kb/search")
async def search_kb(request: KBSearchRequest):
    """
    Semantic search in a knowledgebase.

    Retrieves top-k most relevant documents using vector similarity.
    """
    try:
        logger.info(f"Searching KB for account_id={request.account_id}, kb_id={request.kb_id}, query='{request.query}'")

        collection_name = f"kb_{request.kb_id}"

        # Search using existing RAG function (blocking - run in thread pool).
        # Pass ``collection_name`` as a keyword: the 4th positional param on
        # get_matching_documents is ``module``, which made the old positional
        # call treat "kb_<id>" as a module filter — no collection has that
        # module tag, so every KB search returned zero hits.
        results, token_usage, _ = await asyncio.to_thread(
            get_matching_documents,
            request.query,
            request.k,
            request.account_id,
            collection_name=collection_name,
        )

        logger.info(f"KB search returned {len(results)} results for query: {request.query[:50]}")

        return {
            "status": "success",
            "results": results,
            "token_usage": token_usage,
            "collection": collection_name,
        }

    except Exception as e:
        logger.exception(f"Error searching KB: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to search KB: {str(e)}")


@router.post("/kb/retrigger_integration")
def retrigger_integration_kb(request: KBRetriggerIntegrationRequest):
    """Re-scrape a single integration's knowledge base.

    Dispatches to the right scraper (Confluence/ServiceNow) in a background
    thread and returns immediately. The scraper takes a per-integration lock,
    so an overlapping periodic sync just makes this a no-op — still reported
    as success, since a sync is in progress either way.
    """
    from rag.core.documents.scraper import load_confluence_docs, load_servicenow_kb

    source = (request.kb_source or "").lower()
    if source not in ("confluence", "servicenow"):
        raise HTTPException(status_code=400, detail=f"Unsupported kb_source: {request.kb_source}")

    # account_ids stays None on purpose: integration_ids is the precise filter.
    # Passing the account would make the scraper's tenant-level account filter
    # skip the integration's own tenant whenever that account isn't in the
    # tenant's active-accounts set — exactly the integration we want to scrape.
    account_ids = None
    integration_ids = [request.integration_id]
    triggered_by = request.triggered_by

    def run():
        try:
            logger.info(f"Retriggering integration KB scrape: integration={request.integration_id}, source={source}")
            if source == "confluence":
                load_confluence_docs(
                    account_ids,
                    True,
                    integration_ids=integration_ids,
                    trigger_type="user_retrigger",
                    triggered_by=triggered_by,
                )
            else:
                load_servicenow_kb(
                    account_ids,
                    True,
                    integration_ids=integration_ids,
                    trigger_type="user_retrigger",
                    triggered_by=triggered_by,
                )
        except Exception as e:
            logger.exception(f"Integration KB retrigger failed for {request.integration_id}: {e}")

    threading.Thread(target=run, daemon=True).start()
    return {"status": "Integration knowledge base sync started", "integration_id": request.integration_id}
