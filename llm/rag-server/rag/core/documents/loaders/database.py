"""
Database document loaders.

Handles loading documents from database tables (events, recommendations).
"""

import logging
import time
from contextlib import closing
from typing import List

from rag.core.documents.loaders.file_loaders import SQLDatabase, SQLDatabaseLoader
from rag.core.types import Document
from sqlalchemy import create_engine

from rag.core.documents.loaders.base import (
    page_content_default_mapper,
    get_active_accounts,
)
from rag.core.documents.processing import process_documents, handle_updated_documents
from rag.core.embeddings.generator import get_embeddings
from utils.config import DBConfig, Module

logger = logging.getLogger(__name__)


def load_event_documents(db, account_id) -> List[Document]:
    """
    Load event documents from the database for a specific account.

    Args:
        db: SQLDatabase instance
        account_id: Account ID to filter events

    Returns:
        List of Document objects containing event data
    """
    # Use parameterized query to prevent SQL injection
    events_query = (
        "SELECT DISTINCT ON (fingerprint) id, finding_id, title, description, source, aggregation_key,"
        " failure,"
        " finding_type, category, priority, subject_type, subject_name, subject_namespace, subject_node, service_key,"
        " cluster, status, principal, subject_owner, subject_owner_kind FROM events"
        " WHERE cloud_account_id = :account_id ORDER BY fingerprint, created_at DESC limit 1;"
    )

    logger.info(f"Loading event documents for account_id: {account_id}")
    events_documents = SQLDatabaseLoader(
        query=events_query,
        db=db,
        page_content_mapper=page_content_default_mapper,
        params={"account_id": account_id},
    ).load()
    return events_documents


def load_recommendation_documents(db, account_id) -> List[Document]:
    """
    Load recommendation documents from the database for a specific account.

    Args:
        db: SQLDatabase instance
        account_id: Account ID to filter recommendations

    Returns:
        List of Document objects containing recommendation data
    """
    # Use parameterized query to prevent SQL injection
    recommendations_query = (
        "SELECT * FROM recommendation_view"
        " WHERE cloud_account_id = :account_id"
        " ORDER BY created_at DESC limit 100;"
    )
    logger.info(f"Loading recommendation documents for account_id: {account_id}")
    recommendations_documents = SQLDatabaseLoader(
        query=recommendations_query,
        db=db,
        page_content_mapper=page_content_default_mapper,
        params={"account_id": account_id},
    ).load()
    return recommendations_documents


def load_documents(loader_func, module, db, embeddings, account_id):
    """
    Generic function to load and process documents from database.

    Args:
        loader_func: Function that loads documents (load_event_documents or load_recommendation_documents)
        module: Module name (events, recommendations)
        db: SQLDatabase instance
        embeddings: Embedding function
        account_id: Account ID
    """
    logger.info(f"Loading {module} documents from db for account_id: {account_id}")
    documents = loader_func(db, account_id)
    logger.info(f"Processing documents for {module} for account_id: {account_id}")
    start_time = time.time()
    collection_name, documents_id = process_documents(documents, embeddings, account_id, module=module)
    if documents_id and collection_name:
        handle_updated_documents(collection_name, documents_id)
    logger.info(
        f"Embeddings generated for account id {account_id}, module {module}, collection {collection_name} "
        f"in {time.time() - start_time} sec"
    )


def load_docs_from_db(module=None):
    """
    Load documents from database for all active accounts.

    This is the main entry point for database document loading.
    Fetches active K8s accounts and loads events/recommendations for each.

    Args:
        module: Optional module filter (events, recommendations, or None for both)
    """
    engine = create_engine(DBConfig.url)
    try:
        with closing(engine.raw_connection()) as connection:
            with closing(connection.cursor()) as cursor:
                logger.info(f"Fetching active accounts from db for module: {module}")
                active_accounts = get_active_accounts(cursor)
                logger.info(f"{len(active_accounts)} active accounts fetched from db, Accounts: {active_accounts}")
                for row in active_accounts:
                    account_id = row[0]
                    if account_id:
                        logger.info(f"Loading documents for account_id: {account_id}, module: {module}")
                        db = SQLDatabase(
                            include_tables=["events", "recommendation_view"],
                            view_support=True,
                            engine=engine,
                        )
                        if module == Module.events.value:
                            load_documents(
                                load_event_documents,
                                module,
                                db,
                                get_embeddings(account_id),
                                account_id,
                            )
                        elif module == Module.recommendations.value:
                            load_documents(
                                load_recommendation_documents,
                                module,
                                db,
                                get_embeddings(account_id),
                                account_id,
                            )
                        else:
                            # Load both events and recommendations if no module specified
                            load_documents(
                                load_event_documents,
                                Module.events.value,
                                db,
                                get_embeddings(account_id),
                                account_id,
                            )
                            load_documents(
                                load_recommendation_documents,
                                Module.recommendations.value,
                                db,
                                get_embeddings(account_id),
                                account_id,
                            )
    finally:
        engine.dispose()
