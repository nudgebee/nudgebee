# RAG (Retrieval Augmented Generation) Server

## Overview

The RAG Server is a Python-based application that provides functionalities for building and querying a knowledge base for Retrieval Augmented Generation. It uses **FastAPI** for its API, **Langchain** for orchestrating RAG pipelines, **Qdrant** as the primary vector store (with ChromaDB migration support), and various LLMs for embedding and reranking.

**Key Features:**
- 🚀 **FastAPI + Uvicorn** - Modern async framework with automatic API docs
- 🗄️ **Qdrant Vector Database** - Memory-optimized storage with disk-backed vectors
- 🔄 **Lazy Loading** - ChromaDB only loaded when needed (migration scenarios)
- 📊 **Memory Optimized** - Reduced from 3GB → 1.3GB RAM usage
- 🔐 **Single-Process Architecture** - Uvicorn single worker (avoids Qdrant file locking)

Data sources include static files (JSON, CSV for Prometheus, Loki, AWS, GCP, etc.), a PostgreSQL database (for events and recommendations), S3 (for PDF documents like Kubernetes documentation), and dynamic data uploaded via API for specific accounts and modules.

## Architecture

### Single-Process Design

The server runs as a **single Uvicorn worker** to avoid file locking issues with Qdrant's persistent storage mode.

**Framework**: FastAPI + Uvicorn (async ASGI server)

**Responsibilities**:
- Exposing all public API endpoints (`/load_docs`, `/get_matching_doc`, etc.)
- Handling all document ingestion, processing, and embedding
- Writing data to Qdrant collections
- Performing vector similarity searches directly (no separate search service needed)
- Health checks at `/health`
- Auto-generated API docs at `/docs` (Swagger UI) and `/redoc`

### Vector Database Architecture

**Primary:** Qdrant (memory-mapped mode)
- Thread-safe, single shared client
- On-disk storage with memory mapping for reduced RAM usage
- All 47 collections configured for `on_disk=True`
- Persistent storage at `/nudgebee/rag-server/db`

**Legacy:** ChromaDB (migration support only)
- Only loaded when `VECTOR_DB=chromadb` or during migration
- Lazy-loaded to prevent memory leaks
- Migration tools available at `/api/migrate/chroma-to-qdrant/*`

**Memory Optimization:**
- ChromaDB lazy loading: Saves ~600MB-1GB
- Qdrant memory-mapped storage: Saves ~1.1GB
- Total reduction: 3GB → 1.3GB (57% savings)

### Key Architectural Changes

**Before (Flask + Gunicorn):**
```
┌─────────────────────────────────────┐
│  Web Service (Gunicorn + gevent)   │
│  - Multiple workers                 │
│  - Port 9999 (main API)            │
│  - Port 8081 (health)              │
│  └─> TCP Socket → Search Service   │
└─────────────────────────────────────┘
         ↓ IPC
┌─────────────────────────────────────┐
│  Search Service (Separate Process)  │
│  - ChromaDB searches on port 10000  │
│  - Auto-restart after N searches    │
└─────────────────────────────────────┘
```

**After (FastAPI + Uvicorn):**
```
┌─────────────────────────────────────┐
│  FastAPI Server (Uvicorn)          │
│  - Single worker (thread-safe)     │
│  - Port 9999 (all endpoints)       │
│  - Async I/O for concurrency       │
│  - Direct Qdrant searches          │
│  - Health at /health               │
└─────────────────────────────────────┘
         ↓
┌─────────────────────────────────────┐
│  Qdrant (Memory-Mapped Storage)    │
│  - On-disk vectors with mmap       │
│  - Single shared client            │
│  - File-based persistent storage   │
└─────────────────────────────────────┘
```

## Features

### Core Functionality

- **Single-Process Architecture:** Simplified, async design using FastAPI + Uvicorn
- **Memory-Optimized Vector Storage:** Qdrant with disk-backed vectors (1.3GB RAM for 47 collections)
- **Document Ingestion:**
  - Loads documents from diverse sources: PostgreSQL, local JSON/CSV files, S3 (PDFs), and direct API uploads
  - Supports various data formats (text, JSON, CSV, PDF, XML)
  - Asynchronous document loading and embedding generation
  - File-based locking to manage concurrent data loading operations
- **Embedding Generation:**
  - Utilizes a wide range of embedding models via Langchain integrations (Bedrock, Azure OpenAI, OpenAI, HuggingFace, Ollama, Sagemaker, GoogleAI, VertexAI)
  - Configurable embedding provider and model via environment variables
  - Semantic chunking for PDF documents
- **Vector Storage:**
  - Primary: **Qdrant** with memory-mapped storage for low RAM usage
  - Legacy: **ChromaDB** (migration support only, lazy-loaded)
  - Manages collections based on `module` and `account_id` for multi-tenancy
- **RAG Pipeline:**
  - Semantic search across document collections
  - Multi-collection search with result aggregation
  - Query expansion and reranking using LLMs
  - Configurable top-k retrieval
- **API Documentation:**
  - Auto-generated Swagger UI at `/docs`
  - ReDoc at `/redoc`
  - OpenAPI schema at `/openapi.json`

### Migration Tools

- **ChromaDB → Qdrant Migration API:**
  - `POST /api/migrate/chroma-to-qdrant/start` - Start migration
  - `GET /api/migrate/chroma-to-qdrant/status` - Check migration status
  - Preserves all collections, metadata, and vectors
  - See `docs/MIGRATION_GUIDE.md` for details

## Environment Variables

### Core Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `VECTOR_DB` | Vector database to use (`qdrant` or `chromadb`) | `qdrant` | No |
| `RAGSERVER_PORT` | Port for the FastAPI server | `9999` | No |
| `UVICORN_WORKERS` | Number of Uvicorn workers (use 1 for Qdrant) | `1` | No |
| `UVICORN_TIMEOUT` | Keep-alive timeout in seconds | `180` | No |
| `LOGGING_CONFIG_FILE` | Path to custom logging config JSON | `config/logging.json` | No |

### Embedding Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `EMBEDDING_PROVIDER` | Embedding provider (`bedrock`, `azure`, `openai`, etc.) | `bedrock` | No |
| `EMBEDDING_MODEL` | Model name for embeddings | Provider-specific | No |
| `EMBEDDING_ENDPOINT_URL` | Custom endpoint URL | None | For custom providers |

### Database Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `DATABASE_URL` | PostgreSQL connection URL | - | Yes (for DB loading) |
| `S3_BUCKET_NAME` | S3 bucket for document storage | - | Yes (for S3 loading) |

### Performance Tuning

| Variable | Description | Default |
|----------|-------------|---------|
| `EMBEDDING_BATCH_SIZE` | Batch size for embedding generation | `10` |
| `MAX_CONCURRENCY` | Max concurrent embedding tasks | `5` |
| `INITIAL_WAIT_TIME` | Initial retry wait time (seconds) | `1` |
| `MAX_RETRY_DURATION` | Max retry duration (seconds) | `600` |

## API Endpoints

### Health & Status

- `GET /health` - Health check endpoint
- `GET /docs` - Swagger UI (interactive API documentation)
- `GET /redoc` - ReDoc (alternative API documentation)

### Document Loading

- `POST /load_docs` - Load documents from configured sources
- `POST /load_docs/{module}` - Load documents for specific module
- `POST /load_account_module_docs` - Load documents for specific account and module
- `POST /load_tenant_knowledge_base_docs` - Load tenant-specific knowledge base

### Document Search

- `GET /get_matching_doc` - Search documents across collections
- `POST /api/rag/query` - Advanced RAG query with reranking

### Collection Management

- `GET /collections` - List all collections
- `GET /collections/{collection_name}` - Get collection details
- `GET /collections/{collection_name}/query` - Query a specific collection
- `GET /collections/{collection_name}/download` - Download collection data
- `DELETE /collections` - Delete all collections
- `DELETE /collections/{collection_name}` - Delete a specific collection
- `DELETE /collections/module/{module_name}` - Delete collections for a module
- `POST /admin/configure-ondisk` - Optimize collections for disk storage

### Migration (ChromaDB → Qdrant)

- `POST /api/migrate/chroma-to-qdrant/start` - Start migration
- `GET /api/migrate/chroma-to-qdrant/status` - Get migration status

## Prerequisites

- Python >= 3.10
- UV >= 0.1.0 for dependency management
- API keys and credentials for your chosen embedding model provider and LLM provider
- (Optional) PostgreSQL database credentials
- (Optional) AWS S3 credentials

## Installation

### 1. Install UV

```bash
pip install uv
```

### 2. Clone the repository and navigate to the directory

```bash
git clone <repository-url>
cd llm/rag-server
```

### 3. Install dependencies

```bash
uv pip install -e ".[dev,test]"
```

## Managing Dependencies

### Adding or Updating Dependencies

1. Manually edit `pyproject.toml` to add or update dependencies
2. Regenerate the lock file:
   ```bash
   uv pip compile pyproject.toml --extra dev --extra test -o uv.lock
   ```
3. Install the updated dependencies:
   ```bash
   uv pip sync uv.lock
   ```

## Running the Server

### Local Development

**Setup:**
```bash
# Install dependencies
cd llm/rag-server
uv pip install -e ".[dev,test]"

# Set environment variables
export VECTOR_DB=qdrant
export RAGSERVER_PORT=9999
export LOGGING_CONFIG_FILE=.nudgebee/logging.json  # For colored logs

# Run server
python server.py

# Or with uvicorn directly
uvicorn server:app --reload --port 9999
```

**Access:**
- API: http://localhost:9999
- Swagger UI: http://localhost:9999/docs
- ReDoc: http://localhost:9999/redoc

### Docker

**Build:**
```bash
docker build -t rag-server:latest .
```

**Run:**
```bash
docker run -p 9999:9999 \
  -e VECTOR_DB=qdrant \
  -e UVICORN_WORKERS=1 \
  -v /path/to/db:/nudgebee/rag-server/db \
  rag-server:latest web
```

**Environment variables can be passed via `-e` flag or `--env-file`.**

### Kubernetes

The server is deployed as a StatefulSet to maintain persistent storage for Qdrant.

**Deploy:**
```bash
helm upgrade rag-server ./deploy/kubernetes/rag-server \
  -f ./deploy/kubernetes/rag-server/values-prod.yaml \
  --set image.tag=<version> \
  --install --namespace nudgebee
```

**Key configurations:**
- Single replica (StatefulSet)
- Persistent volume for Qdrant storage
- Resource limits: CPU 2000m, Memory 2Gi
- Liveness/Readiness probes on `/health`

## Development

### Project Structure

```
llm/rag-server/
├── server.py                 # FastAPI app entry point
├── entrypoint.sh            # Docker entrypoint (starts uvicorn)
├── pyproject.toml           # Python dependencies
├── uv.lock                  # Locked dependencies
├── Dockerfile               # Multi-stage Docker build
├── Makefile                 # Build commands (lint, test, run)
├── config/                  # Configuration files
│   ├── logging.json         # Production logging config
│   └── setup_logger.py      # Logging setup utilities
├── controllers/             # FastAPI route controllers
│   ├── health.py            # Health check endpoints
│   ├── collection_controller.py  # Collection management
│   ├── document_controller.py # Document ingestion
│   ├── search_controller.py # Search endpoints
│   ├── cache_controller.py  # Cache management
│   └── migration_controller.py   # Migration endpoints
├── rag/                     # RAG core logic
│   ├── __init__.py          # RAG module entry
│   ├── exceptions.py        # Unified exceptions
│   ├── vector_store.py      # Vector store abstraction
│   ├── qdrant/              # Qdrant implementation
│   │   └── client.py        # Shared client and optimization
│   ├── core/                # Core RAG functionality
│   │   ├── documents/       # Document management
│   │   │   ├── loaders/     # Modular document loaders
│   │   │   └── processing.py # Optimized batch processing
│   │   ├── embeddings/      # Embedding generation
│   │   └── monitoring/      # Metrics and tracking
│   └── search/              # Search logic
│       └── search_logic.py
├── utils/                   # Utility modules
│   ├── config.py            # Environment configuration
│   └── shared.py            # Shared logic functions
├── data/                    # Static data files
└── docs/                    # Documentation
    ├── FLASK_TO_FASTAPI_MIGRATION.md
    ├── MIGRATION_GUIDE.md
    └── VECTOR_DB_ARCHITECTURE.md
```

### Build Commands

```bash
# Format code
make fmt

# Lint code
make lint

# Run tests
make test

# Run server locally
make run

# Clean artifacts
make clean
```

### Testing

```bash
# Run all tests
pytest

# Run specific test file
pytest tests/test_search.py

# Run with coverage
pytest --cov=rag --cov-report=html
```

## Logging

### Configuration

The server uses structured JSON logging with Datadog integration.

**Log levels:**
- `INFO` - Standard operations, startup/shutdown
- `WARNING` - Deprecations, potential issues
- `ERROR` - Errors that don't stop execution
- `DEBUG` - Detailed diagnostic info (disabled in production)

**Log files:**
- Production: `config/logging.json` (plain JSON)
- Development: `.nudgebee/logging.json` (colored JSON)

**Suppressed logs:**
- `/health` endpoint access logs (filtered out to reduce noise)
- ChromaDB warnings in Qdrant mode

### Example Log Output

```json
{
  "asctime": "2025-11-23 12:20:04,465",
  "levelname": "INFO",
  "filename": "collection.py",
  "lineno": 76,
  "message": "Found 47 collections in qdrant",
  "dd.trace_id": "0",
  "dd.span_id": "0",
  "dd.service": "rag-server"
}
```

## Performance & Memory

### Memory Optimization

**Techniques applied:**
1. **Lazy Loading ChromaDB** - Only loaded when `VECTOR_DB=chromadb`
2. **Qdrant Memory-Mapped Storage** - Vectors on disk, not RAM
3. **Single Worker** - Avoids process overhead
4. **Unified Exception Handling** - No top-level ChromaDB imports

**Results:**
| Configuration | Memory Usage |
|---------------|--------------|
| Flask + Gunicorn + ChromaDB loaded + Qdrant | 3.0GB |
| FastAPI + Qdrant (all in RAM) | 2.4GB |
| **FastAPI + Qdrant (memory-mapped)** | **1.3GB** |

### Performance Characteristics

- **Query Latency:** ~100-200ms (with memory-mapped storage)
- **Throughput:** ~50-100 queries/sec (async FastAPI)
- **Concurrency:** Handled via async I/O (no worker pool needed)
- **Startup Time:** ~15-20 seconds (loading 47 collections)

## Troubleshooting

### Common Issues

**1. High Memory Usage**
- Ensure `VECTOR_DB=qdrant` (not `chromadb`)
- Check logs for `"Configuring memory-mapped storage"` message
- Verify `UVICORN_WORKERS=1` (multiple workers = memory duplication)

**2. Slow Queries**
- Memory-mapped storage trades speed for RAM (~10-20% slower)
- For faster queries, increase available RAM and reduce `memmap_threshold`

**3. File Locking Errors**
- Use single worker: `UVICORN_WORKERS=1`
- Ensure no other processes accessing Qdrant storage

**4. Migration Failures**
- Check both ChromaDB and Qdrant directories exist
- Verify sufficient disk space
- See `docs/MIGRATION_GUIDE.md` for detailed steps

### Debug Mode

```bash
# Enable debug logging
export LOG_LEVEL=DEBUG

# Run with auto-reload
uvicorn server:app --reload --log-level debug
```

### Health Checks

```bash
# Check server health
curl http://localhost:9999/health

# Expected response
{"status": "ok"}

# Check collections
curl http://localhost:9999/collections
```

## Migration from ChromaDB to Qdrant

**See detailed guide:** `docs/MIGRATION_GUIDE.md`

**Quick steps:**
1. Backup ChromaDB data
2. Start migration: `POST /api/migrate/chroma-to-qdrant/start`
3. Monitor status: `GET /api/migrate/chroma-to-qdrant/status`
4. Update `VECTOR_DB=qdrant` in environment
5. Restart server

## Documentation

- **API Docs:** http://localhost:9999/docs (Swagger UI)
- **Alternative Docs:** http://localhost:9999/redoc (ReDoc)
- **Migration Guide:** `docs/MIGRATION_GUIDE.md`
- **Flask to FastAPI Migration:** `docs/FLASK_TO_FASTAPI_MIGRATION.md`
- **Vector DB Architecture:** `docs/VECTOR_DB_ARCHITECTURE.md`

## Contributing

1. Create feature branch from `main`
2. Make changes and add tests
3. Run `make validate` (fmt + lint + test)
4. Create PR to `main`
5. After merge, auto-PR to `test`, then manual PR to `prod`

## License

Internal Nudgebee project.

---

## Development Context

### Project Overview
The **RAG Server** is a memory-optimized Python application responsible for document ingestion, embedding, and retrieval augmented generation.

### Key Technologies
- **Language:** Python 3.11+
- **Framework:** FastAPI
- **Vector Database:** Qdrant (primary, memory-mapped storage)
- **Embedding:** Langchain (supporting AWS Bedrock, OpenAI, and GoogleAI)
- **Infrastructure:** Single-process architecture via Uvicorn (avoids Qdrant locking).

### Development Conventions
- **Build System:** **UV** (dependency management and build workflow).
- **Linting:** Enforced via `black` (line-length 120), `flake8`, and `mypy` via UV.
- **Validation:** Run `make lint` and `make test` (invoking UV internally) before pushing changes.
- **Testing:** Unit and integration tests using `pytest`.
- **Performance:** Optimized from 3GB to 1.3GB RAM using Qdrant's disk-backed vectors.