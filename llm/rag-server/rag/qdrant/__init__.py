"""Qdrant vector database client module.

Provides connection and operations for Qdrant vector database.
"""

from .client import get_qdrant_client

__all__ = ["get_qdrant_client"]
