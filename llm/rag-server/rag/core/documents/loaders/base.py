"""
Base utilities for document loaders.

Common helper functions used across all document loaders.
"""

import csv
import logging
import sys
from pathlib import Path
from typing import List, Optional

import tiktoken
from sqlalchemy import RowMapping

from utils.config import Config

csv.field_size_limit(sys.maxsize)
logger = logging.getLogger(__name__)


def trim_text(text: str, max_length: int = Config.max_token_length):
    """Trim text to maximum token length using tiktoken."""
    try:
        tokenizer = tiktoken.get_encoding("cl100k_base")
        tokens = tokenizer.encode(text)
        trimmed_tokens = tokens[: max_length - 1]
        trimmed_text = tokenizer.decode(trimmed_tokens)
    except Exception as e:
        logger.error(f"Error while trimming text: {str(e)}")
        trimmed_text = text
    return trimmed_text


def page_content_default_mapper(row: RowMapping, column_names: Optional[List[str]] = None) -> str:
    """
    Convert a database record into a page content string.

    Args:
        row: Database row mapping
        column_names: Optional list of columns to include

    Returns:
        Formatted string with column: value pairs
    """
    backslash4 = "\\\\"
    backslash2 = "\\"
    if column_names is None:
        column_names = list(row.keys())
    return "\n".join(
        f"{column}: {' '.join(str(value).replace(backslash4, backslash2).split())}"
        for column, value in row.items()
        if column in column_names
    )


def get_active_accounts(cursor):
    """
    Get active Kubernetes accounts from database.

    Args:
        cursor: Database cursor

    Returns:
        List of active account IDs
    """
    query = (
        "select distinct ca.id from cloud_accounts ca inner join agent a on ca.id = a.cloud_account_id "
        "where ca.status = 'active' and a.status = 'CONNECTED' and ca.account_type='kubernetes'"
    )
    cursor.execute(query)
    result = cursor.fetchall()
    return result


def chunk_data(data, chunk_size):
    """
    Generator to chunk data dictionary into smaller batches.

    Args:
        data: Dictionary with ids, documents, metadatas, embeddings keys
        chunk_size: Size of each chunk

    Yields:
        Dictionary chunks
    """
    for i in range(0, len(data["ids"]), chunk_size):
        yield {
            "ids": data["ids"][i : i + chunk_size],
            "documents": data["documents"][i : i + chunk_size],
            "metadatas": data["metadatas"][i : i + chunk_size],
            "embeddings": data["embeddings"][i : i + chunk_size],
        }


def load_and_filter_files(file_paths: List[Path]) -> List[Path]:
    """Filter file paths to only existing files."""
    file_paths_exists = []
    for file_path in file_paths:
        if file_path.exists():
            file_paths_exists.append(file_path)
        else:
            logger.error(f"File not found: {file_path}")
    return file_paths_exists
