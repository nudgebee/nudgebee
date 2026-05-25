"""
Document loaders package.

Re-exports all loader functions for backward compatibility.
"""

# Base utilities
from rag.core.documents.loaders.base import (
    trim_text,
    page_content_default_mapper,
    get_active_accounts,
    chunk_data,
    load_and_filter_files,
)

# Database loaders
from rag.core.documents.loaders.database import (
    load_event_documents,
    load_recommendation_documents,
    load_documents,
    load_docs_from_db,
)

# Observability loaders
from rag.core.documents.loaders.observability import (
    load_prom_docs,
    load_prom_json_docs,
    load_nb_prom_metrics_metadata,
    load_loki_docs,
    handle_account_specific_docs,
)

# Code tool loaders
from rag.core.documents.loaders.code_tools import (
    load_planner_docs,
)

# Document loaders
from rag.core.documents.loaders.documents import (
    load_traces_docs,
    load_events_json_docs,
    load_recommendations_json_docs,
    load_pdf_docs,
)

# Account-specific loaders
from rag.core.documents.loaders.account import (
    load_account_module_docs,
    load_tenant_knowledge_base_docs,
)

__all__ = [
    # Base utilities
    "trim_text",
    "page_content_default_mapper",
    "get_active_accounts",
    "chunk_data",
    "load_and_filter_files",
    # Database loaders
    "load_event_documents",
    "load_recommendation_documents",
    "load_documents",
    "load_docs_from_db",
    # Observability loaders
    "load_prom_docs",
    "load_prom_json_docs",
    "load_nb_prom_metrics_metadata",
    "load_loki_docs",
    "handle_account_specific_docs",
    # Code tool loaders
    "load_planner_docs",
    # Document loaders
    "load_traces_docs",
    "load_events_json_docs",
    "load_recommendations_json_docs",
    "load_pdf_docs",
    # Account-specific loaders
    "load_account_module_docs",
    "load_tenant_knowledge_base_docs",
]
