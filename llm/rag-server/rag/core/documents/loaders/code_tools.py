"""
Code-related document loaders.

Handles loading documents for code management and planning tools: GitHub, Planner.
"""

import logging
from typing import List, Optional

from rag.core.documents.loaders.file_loaders import JSONLoader

from rag.core.documents.processing import process_documents, handle_updated_documents
from rag.core.embeddings.generator import get_embeddings

logger = logging.getLogger(__name__)


def load_planner_docs(account_ids: Optional[List[str]] = None, force: bool = False):
    """
    Load Planner JSON documents for global collection.

    Args:
        account_ids: Not currently used (planner docs are global)
        force: Not currently used
    """
    logger.info("Processing planner documents...")
    logger.info(f"Force param is not used in this function. Force:{force}")
    logger.info(f"Account ids: {account_ids} is not used in this function.")
    logger.info("Processing documents for module: planner")
    file_path = "./data/planner.json"
    loader = JSONLoader(file_path=file_path, jq_schema=".[]", text_content=False)
    documents = loader.load()
    collection_name, documents_id = process_documents(documents, get_embeddings(""), account_id=None, module="planner")
    if documents_id and collection_name:
        handle_updated_documents(collection_name, documents_id)
