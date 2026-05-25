"""
Document loaders for various file types.

Handles loading documents from various file formats: JSON, PDF, traces, events, recommendations, Kubernetes docs.
"""

import gc
import logging
import os
import time
from typing import List, Optional

import pymupdf
from rag.core.documents.loaders.file_loaders import JSONLoader
from rag.core.types import Document

from rag.core.documents.loaders.semantic_chunker import SemanticChunker
from rag.core.documents.processing import process_documents, handle_updated_documents
from rag.core.embeddings.generator import get_embeddings
from utils.config import Config
from utils.shared import s3_client

logger = logging.getLogger(__name__)


def load_traces_docs():
    """Load traces JSON documents for global collection."""
    module = "traces"
    logger.info(f"Loading documents for module: {module}")
    file_path = "./data/traces.json"
    loader = JSONLoader(file_path=file_path, jq_schema=".[]", text_content=False)
    documents = loader.load()
    collection_name, documents_id = process_documents(documents, get_embeddings(""), account_id=None, module=module)
    if documents_id and collection_name:
        handle_updated_documents(collection_name, documents_id)
    logger.info(f"Documents loaded for module: {module}, collection: {collection_name}")


def load_events_json_docs(account_ids: Optional[List[str]] = None, force: bool = False):
    """
    Load events JSON documents for global collection.

    Args:
        account_ids: Not currently used (events docs are global)
        force: Not currently used
    """
    module = "events"
    logger.info(f"Loading documents for module: {module}")
    logger.info(f"Force param is not used in this function. Force:{force}")
    logger.info(f"Account ids: {account_ids} is not used in this function.")
    file_path = "./data/events.json"
    loader = JSONLoader(file_path=file_path, jq_schema=".[]", text_content=False)
    documents = loader.load()
    collection_name, documents_id = process_documents(documents, get_embeddings(""), account_id=None, module=module)
    if documents_id and collection_name:
        handle_updated_documents(collection_name, documents_id)
    logger.info(f"Documents loaded for module: {module}, collection: {collection_name}")


def load_recommendations_json_docs(account_ids: Optional[List[str]] = None, force: bool = False):
    """
    Load recommendations JSON documents for global collection.

    Args:
        account_ids: Not currently used (recommendations docs are global)
        force: Not currently used
    """
    module = "recommendations"
    logger.info(f"Loading documents for module: {module}")
    logger.info(f"Force param is not used in this function. Force:{force}")
    logger.info(f"Account ids: {account_ids} is not used in this function.")
    file_path = "./data/recommendation.json"
    loader = JSONLoader(file_path=file_path, jq_schema=".[]", text_content=False)
    documents = loader.load()
    collection_name, documents_id = process_documents(documents, get_embeddings(""), account_id=None, module=module)
    if documents_id and collection_name:
        handle_updated_documents(collection_name, documents_id)
    logger.info(f"Documents loaded for module: {module}, collection: {collection_name}")


def _get_pdf_file_path(file_name: str) -> str:
    """Get the PDF file path, downloading from S3 if necessary."""
    data_file_path = f"./data/{file_name}"
    if os.path.exists(data_file_path):
        logger.info(f"File found locally: {data_file_path}")
        return data_file_path

    try:
        bucket_name = Config.s3_bucket_name
        logger.info(f"File not found locally. Downloading from S3: bucket={bucket_name}, key={file_name}")
        s3_client().download_file(bucket_name, file_name, data_file_path)
        logger.info(f"File downloaded successfully from S3: {data_file_path}")
        return data_file_path
    except Exception as e:
        logger.error(f"Failed to download file from S3: {e}")
        raise


def _process_pdf_batch(reader, start_idx: int, end_idx: int, file_path: str) -> list:
    """Process a batch of PDF pages and return documents."""
    documents = []
    for idx in range(start_idx, end_idx):
        text = reader.load_page(idx).get_text()
        if text:
            documents.append(Document(page_content=text, metadata={"source": file_path, "page": idx + 1}))
    return documents


def _create_semantic_chunks_with_retry(splitter: SemanticChunker, all_texts: List[str]) -> List[Document]:
    """Create semantic chunks with time-based retry mechanism."""
    start_time = time.monotonic()
    wait_time = Config.initial_wait_time
    max_retry_duration = Config.max_retry_duration

    while time.monotonic() - start_time < max_retry_duration:
        try:
            semantic_chunked_docs = splitter.create_documents(all_texts)
            logger.info(f"Chunked into {len(semantic_chunked_docs)} semantic documents.")
            return semantic_chunked_docs
        except Exception as e:
            logger.error(f"Error during semantic chunking: {str(e)}. Retrying in {wait_time}s...")
            time.sleep(wait_time)
            wait_time = min(wait_time * 2, 60)  # Exponential backoff capped at 60 seconds

    logger.error(f"Failed to chunk documents after {max_retry_duration} seconds.")
    return []


def _process_pdf_with_sliding_window(file_path: str, embeddings):
    """Process PDF with sliding window and return document IDs."""
    reader = pymupdf.open(file_path)
    total_pages = len(reader)
    logger.info(f"Total pages in PDF: {total_pages}")

    documents_ids = []
    collection_name = None

    batch_size = 5
    overlap = 1
    start_idx = 0

    while start_idx < total_pages:
        end_idx = min(start_idx + batch_size, total_pages)
        logger.info(f"Processing pages {start_idx + 1} to {end_idx} of {total_pages}...")

        documents = _process_pdf_batch(reader, start_idx, end_idx, file_path)

        splitter = SemanticChunker(embeddings)
        all_texts = [doc.page_content for doc in documents]
        semantic_chunked_docs = _create_semantic_chunks_with_retry(splitter, all_texts)
        chunked_docs = [doc for doc in semantic_chunked_docs if doc.page_content.strip()]
        collection_name, batch_doc_ids = process_documents(chunked_docs, embeddings, account_id=None, module="docs")
        documents_ids.extend(batch_doc_ids)

        start_idx = end_idx - overlap
        if end_idx >= total_pages:
            start_idx = total_pages

        del documents, chunked_docs
        gc.collect()

    if documents_ids and collection_name:
        handle_updated_documents(collection_name, documents_ids)

    reader.close()
    return documents_ids


def load_pdf_docs(file_name: str):
    """
    Load and process a PDF document.

    Downloads from S3 if not found locally, processes with semantic chunking.

    Args:
        file_name: Name of the PDF file (e.g., 'k8s.pdf')
    """
    try:
        file_path = _get_pdf_file_path(file_name)
        _process_pdf_with_sliding_window(file_path, get_embeddings(""))
    except Exception as e:
        logger.error(f"Failed to process the PDF file: {e}")
        raise
