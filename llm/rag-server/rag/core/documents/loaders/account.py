"""
Account-specific document loaders.

Handles loading documents for specific accounts and tenants.
"""

import logging
from typing import Union, Optional

from rag.core.documents.loaders.file_loaders import (
    CSVLoader,
    JSONLoader,
    TextLoader,
    UnstructuredXMLLoader,
)

from rag.core.documents.processing import process_documents
from rag.core.embeddings.generator import get_embeddings
from rag.core.utils.db_query import get_first_active_account_for_tenant

logger = logging.getLogger(__name__)


def load_account_module_docs(
    account_id: str,
    module: str,
    loader: Union[JSONLoader, UnstructuredXMLLoader, CSVLoader, TextLoader],
    doc_id: Optional[str] = None,
    extra_metadata: Optional[dict] = None,
):
    """
    Load documents for a specific account and module.

    Generic loader that accepts any LangChain document loader.

    Args:
        account_id: Account ID
        module: Module name
        loader: LangChain document loader instance (JSONLoader, CSVLoader, etc.)
        doc_id: Optional document ID
        extra_metadata: Optional caller-supplied tags merged into every loaded
            document's metadata (e.g. ``{"_id": ..., "memory_type": ...}``).
            Existing keys on a document (e.g. ``source``, ``line_number`` set by
            LineByLineLoader) are preserved.
    """
    documents = loader.load()
    if doc_id and len(documents) > 0:
        # If multiple documents are loaded (e.g. split lines), we only assign ID to the first one
        # or we might need a better strategy if we want to delete a group of docs.
        # But for memory, it's usually one doc.
        documents[0].id = doc_id

    if extra_metadata:
        for doc in documents:
            existing = doc.metadata or {}
            doc.metadata = {**extra_metadata, **existing}

    collection_name, _ = process_documents(documents, get_embeddings(account_id), account_id, module=module)
    logger.info(f"{module} documents loaded successfully for account_id: {account_id}, collection: {collection_name}")


def load_tenant_knowledge_base_docs(
    tenant_id: str,
    loader: Union[JSONLoader, UnstructuredXMLLoader, CSVLoader, TextLoader],
):
    """
    Load knowledge base documents for a specific tenant.

    Generic loader that accepts any LangChain document loader.

    Args:
        tenant_id: Tenant ID
        loader: LangChain document loader instance (JSONLoader, CSVLoader, etc.)
    """
    documents = loader.load()
    module = "knowledge_base"
    # get_embeddings looks up LLM integrations keyed on cloud_account_id, so we
    # resolve an active account in the tenant first. Any account in the tenant
    # works because LLM configs are typically tenant-shared.
    embedding_account_id = get_first_active_account_for_tenant(tenant_id)
    if not embedding_account_id:
        logger.warning(f"No active account found for tenant {tenant_id}; using tenant_id as embedding key fallback")
        embedding_account_id = tenant_id
    # Tenant-scoped collection: don't pass account_id (would leak as account=global
    # or scope to a single account). Use tenant_id as the scoping axis so every
    # cloud account in the tenant sees this KB via search-time metadata matching.
    collection_name, _ = process_documents(
        documents,
        get_embeddings(embedding_account_id),
        module=module,
        collection_name=f"{tenant_id}_knowledge_base",
        source="user_kb",
        tenant_id=tenant_id,
    )
    logger.info(f"{module} documents loaded successfully for tenant_id: {tenant_id}, collection: {collection_name}")
