"""
Observability and monitoring tool document loaders.

Handles loading documents for monitoring tools: Prometheus, Loki, ElasticSearch.
"""

import gc
import logging
from contextlib import closing
from pathlib import Path
from typing import List, Optional

from rag.core.documents.loaders.file_loaders import CSVLoader, JSONLoader
from rag.core.types import Document
from sqlalchemy import create_engine

from rag.core.documents.loaders.base import get_active_accounts, load_and_filter_files
from rag.core.documents.processing import process_documents, handle_updated_documents
from rag.core.embeddings.generator import get_embeddings
from rag.core.monitoring.metrics import (
    get_update_prometheus_metrics,
    # extract_metric_names removed - was only used for expensive scroll check
    # get_metrics_from_embeddings removed - was only used for expensive scroll check
)
from utils.config import DBConfig
from utils.shared import get_collection_name

logger = logging.getLogger(__name__)


def _load_documents_from_files(file_paths_exists):
    """Helper function to load documents from JSON files."""
    documents_from_json = []
    for file_path in file_paths_exists:
        logger.info(f"Adding prometheus documents from file: {file_path}")
        loader = JSONLoader(file_path=str(file_path), jq_schema=".[]", text_content=False)
        documents_from_json.extend(loader.load())
        gc.collect()  # Clean up after loading documents
    return documents_from_json


def _convert_example_to_document(example):
    """Helper function to convert a metrics example into a Document object."""
    if isinstance(example, dict):
        page_content = example.get("page_content", "")  # Default to empty string
        if not isinstance(page_content, str):
            page_content = str(page_content) if page_content is not None else ""
        return Document(page_content=page_content, metadata=example.get("metadata"))
    elif isinstance(example, str):
        return Document(page_content=example, metadata={})
    else:
        logger.warning(f"Invalid metrics example format: {example}")
        return None


def handle_account_specific_docs(account_id: str, module: str):
    """
    Process account-specific Prometheus metrics examples.

    Fetches account-specific metrics examples from the database and generates embeddings.

    Args:
        account_id: Account ID
        module: Module name (typically 'prometheus')
    """
    # Get account specific metrics examples
    logger.info(f"Fetching metrics examples for account_id: {account_id}")
    metrics_examples = get_update_prometheus_metrics(str(account_id))
    account_example_documents = []

    logger.info(f"Converting metrics examples to Document objects for account_id: {account_id}")
    if metrics_examples:
        # Convert each example to a Document
        for example in metrics_examples:
            doc = _convert_example_to_document(example)
            if doc:
                account_example_documents.append(doc)

    logger.info(
        f"Generating embeddings for new metrics examples for account_id: {account_id} "
        f"of size {len(account_example_documents)} based on account specific prometheus metric data"
    )
    process_documents(account_example_documents, get_embeddings(account_id), account_id, module=module)
    del account_example_documents
    del metrics_examples
    gc.collect()


def load_prom_docs():
    """
    Load Prometheus CSV documents for all active accounts.

    Loads prometheus.csv file and generates embeddings for each active account.
    """
    engine = create_engine(DBConfig.url)
    try:
        with closing(engine.raw_connection()) as connection:
            with closing(connection.cursor()) as cursor:
                logger.info("Fetching active accounts from db")
                active_accounts = get_active_accounts(cursor)
                file_path = "./data/prometheus.csv"
                loader = CSVLoader(
                    file_path=file_path,
                    csv_args={
                        "delimiter": ",",
                        "quotechar": '"',
                        "fieldnames": ["question", "answer"],
                    },
                )
                documents = loader.load()
                for row in active_accounts:
                    account_id = row[0]
                    logger.info(f"Processing documents for prometheus for account_id: {account_id}")
                    process_documents(
                        documents,
                        get_embeddings(account_id),
                        account_id,
                        module="prometheus",
                    )
                    logger.info(f"Documents loaded successfully for account_id: {account_id}")
    finally:
        engine.dispose()


def load_prom_json_docs(account_ids: Optional[List[str]] = None, force: bool = False):
    """
    Load Prometheus JSON documents for global and account-specific collections.

    This function:
    1. Loads prometheus.json file
    2. Creates global collection with new metrics
    3. For each active account, processes account-specific metrics examples

    Args:
        account_ids: Optional list of account IDs to process (None = all active accounts)
        force: Force reprocessing even if metrics exist in global collection
    """
    logger.info("Loading prometheus documents from file...")
    file_paths = [Path("./data/prometheus.json")]
    module = "prometheus"

    # Load and process files
    file_paths_exists = load_and_filter_files(file_paths)
    documents_from_json = _load_documents_from_files(file_paths_exists)

    # NOTE: Removed expensive metrics check that scrolled through entire collection
    # Old code: get_metrics_from_embeddings() - scrolled 15,843 docs taking ~30-60 seconds
    # New approach: Use upsert which handles duplicates automatically

    # Check if we should process global documents
    should_process = force  # Always process if force=True

    if not force:
        # Check if collection exists (fast operation)
        from rag.qdrant.client import get_qdrant_client
        from rag.exceptions import is_not_found_error

        try:
            client = get_qdrant_client()
            collection_name = get_collection_name(None, module)
            assert collection_name is not None
            collection_info = client.get_collection(collection_name)
            points_count = collection_info.points_count or 0

            if points_count == 0:
                logger.info(f"Collection {collection_name} is empty, processing all documents")
                should_process = True
            else:
                logger.info(
                    f"Collection {collection_name} has {points_count} documents, skipping (use force=True to reprocess)"
                )
                should_process = False
        except Exception as e:
            if is_not_found_error(e):
                logger.info(f"Collection {collection_name} does not exist, processing all documents")
                should_process = True
            else:
                logger.warning(f"Error checking collection: {e}")
                should_process = False

    # Process global documents if needed
    if should_process:
        logger.info(f"Processing {len(documents_from_json)} documents for global account")
        collection_name, documents_id = process_documents(documents_from_json, get_embeddings(""), "", module=module)
        if documents_id and collection_name:
            handle_updated_documents(collection_name, documents_id)

    # Clean up memory
    del documents_from_json
    gc.collect()

    # Process for each account
    engine = create_engine(DBConfig.url)
    try:
        with closing(engine.raw_connection()) as connection:
            with closing(connection.cursor()) as cursor:
                active_accounts = get_active_accounts(cursor)
                logger.info(f"{len(active_accounts)} active accounts fetched from db, Accounts: {active_accounts}")
                for row in active_accounts:
                    account_id = row[0]
                    logger.info(f"Loading documents for account_id: {account_id}, module: {module}")
                    if account_ids and (str(account_id) not in account_ids):
                        logger.info(f"Skipping account_id: {account_id} as it is not in the provided list.")
                        continue
                    handle_account_specific_docs(account_id, module)
                    logger.info(f"Documents loaded successfully for account_id: {account_id}")
                    gc.collect()
    finally:
        engine.dispose()
        gc.collect()


def load_nb_prom_metrics_metadata():
    """
    Load Prometheus metrics metadata from multiple JSON files.

    Loads metadata files for prometheus metrics from various exporters:
    - Generic prometheus metrics
    - kube-state-metrics
    - node-exporter
    - kubelet

    Processes for all active accounts.
    """
    engine = create_engine(DBConfig.url)
    try:
        with closing(engine.raw_connection()) as connection:
            with closing(connection.cursor()) as cursor:
                logger.info("Fetching active accounts from db at load_nb_prom_metrics_metadata")
                active_accounts = get_active_accounts(cursor)
                # load json file
                files = [
                    "./data/prometheus_metric_metadata.json",
                    "./data/prometheus_metric_metadata_kube_state_metrics.json",
                    "./data/prometheus_metric_metadata_node_exporter.json",
                    "./data/prometheus_metric_metadata_kubelet.json",
                ]

                documents = []
                for file in files:
                    loader = JSONLoader(file_path=file, jq_schema=".[]", text_content=False)
                    documents.extend(loader.load())
                for row in active_accounts:
                    account_id = row[0]
                    logger.info("Processing documents for prometheus metadata...")
                    process_documents(
                        documents,
                        get_embeddings(account_id),
                        account_id,
                        module="prometheus-metadata",
                    )
                    logger.info(f"Prometheus documents loaded successfully for account_id: {account_id}")
    finally:
        engine.dispose()


def load_loki_docs(account_ids: Optional[List[str]] = None, force: bool = False):
    """
    Load Loki CSV documents for all active accounts.

    Args:
        account_ids: Optional list of account IDs to process (None = all active accounts)
        force: Not currently used
    """
    logger.info("Loading loki documents from file...")
    logger.info(f"Force param is not used in this function. Force:{force}")
    engine = create_engine(DBConfig.url)
    try:
        with closing(engine.raw_connection()) as connection:
            with closing(connection.cursor()) as cursor:
                logger.info("Fetching active accounts")
                active_accounts = get_active_accounts(cursor)
                logger.info("Loading documents from file...")
                file_path = "./data/loki.csv"
                loader = CSVLoader(
                    file_path=file_path,
                    csv_args={
                        "delimiter": ",",
                        "quotechar": '"',
                        "fieldnames": ["question", "answer"],
                    },
                )
                documents = loader.load()
                for row in active_accounts:
                    account_id = row[0]
                    if account_ids and (str(account_id) not in account_ids):
                        logger.info(f"Skipping account_id: {account_id} as it is not in the provided list.")
                        continue
                    logger.info("Processing documents...")
                    collection_name, documents_id = process_documents(
                        documents, get_embeddings(account_id), account_id, module="loki"
                    )
                    if documents_id and collection_name:
                        handle_updated_documents(collection_name, documents_id)
                    logger.info(f"Loki documents loaded successfully for account_id: {account_id}")
    finally:
        engine.dispose()
