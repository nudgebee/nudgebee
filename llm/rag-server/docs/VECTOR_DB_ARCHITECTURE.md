# Vector Database Architecture

## Overview

The RAG server uses **Qdrant** as its primary vector database. It is optimized for high-performance similarity search with a low memory footprint through on-disk storage and memory-mapped vectors.

## Folder Structure

```
rag/
├── __init__.py                  # RAG module entry
│
├── qdrant/                      # Qdrant implementation
│   ├── __init__.py
│   └── client.py                # Shared client & optimization logic
│
├── migration/                   # Migration tools
│   ├── __init__.py
│   ├── qdrant_exporter.py       # Export data from Qdrant
│   ├── qdrant_importer.py       # Import data into Qdrant
│   └── qdrant_to_qdrant_migration.py # Embedded → Standalone migration
│
├── core/                        # Core RAG functionality
│   ├── __init__.py
│   │
│   ├── documents/               # Document management
│   │   ├── __init__.py
│   │   ├── loaders/             # Modular document loaders
│   │   ├── collection.py        # Collection management
│   │   ├── processing.py        # Optimized batch processing
│   │   └── scraper.py           # Document scraping (Confluence, etc.)
│   │
│   ├── embeddings/              # Embeddings generation & tracking
│   │   ├── __init__.py
│   │   ├── generator.py         # Embeddings generation
│   │   └── tracker.py           # Embedding usage tracking
│   │
│   ├── monitoring/              # Tracking & monitoring
│   │   ├── __init__.py
│   │   └── metrics.py           # Prometheus metrics updates
│   │
│   └── utils/                   # Internal utilities
│       ├── __init__.py
│       └── db_query.py          # Database queries
│
└── search/                      # Search functionality
    ├── __init__.py
    ├── filters.py               # Metadata filter builders
    └── search_logic.py          # Core search algorithms
```

## How It Works

### Shared Client

The system uses a thread-safe, single shared Qdrant client defined in `rag/qdrant/client.py`. This avoids repeated database initialization and provides better performance.

### Memory Optimization

All collections are configured with `on_disk=True` for both vectors and the HNSW index. This allows the system to handle millions of points with minimal RAM usage (~1.3GB for 47+ collections).

## Usage

### Getting the Client

```python
from rag.qdrant import get_qdrant_client

client = get_qdrant_client()
```

### Core RAG Functionality

```python
# ✅ Import modular loaders
from rag.core.documents import loaders
from rag.core.documents.loaders import load_prom_json_docs

# ✅ Use optimized processing
from rag.core.documents.processing import process_documents

# ✅ Import embeddings modules
from rag.core.embeddings.generator import get_embeddings

# ✅ Search with filters
from rag.search.filters import build_metadata_filter
from rag.search.search_logic import search_collections
```

## Best Practices

### For New Code

1. **Use Modular Loaders**: Add new document sources to `rag/core/documents/loaders/`.
2. **Batch Processing**: Always use `process_documents` from `processing.py` which handles efficient batching and retries.
3. **Metadata Filtering**: Use the utilities in `rag/search/filters.py` to construct Qdrant filters.
4. **Utility Access**: Import configuration from `utils.config` and shared logic from `utils.shared`.

---

**Last Updated:** 2026-01-07
