# RAG Server Documentation

Reference documentation for RAG Server architecture and migrations.

## Migration Guides

- **FLASK_TO_FASTAPI_MIGRATION.md** - Flask → FastAPI migration
  - Why we migrated (file locking, async, performance)
  - Step-by-step migration process
  - Code changes and architectural differences

- **MIGRATION_GUIDE.md** - ChromaDB → Qdrant migration
  - Migration API endpoints and process
  - Rollback procedures
  - Performance comparison

- **VECTOR_DB_ARCHITECTURE.md** - Vector database architecture
  - ChromaDB vs Qdrant implementation
  - Migration strategy
  - Memory optimization techniques

## Current Production Architecture

- **FastAPI + Uvicorn** (single worker, async)
- **Qdrant** (memory-mapped storage, 1.3GB RAM for 47 collections)
- **ChromaDB** (lazy-loaded for migration support only)

## See Also

- [Main README](../README.md) - Complete server documentation
- [API Documentation](http://localhost:9999/docs) - Interactive API docs