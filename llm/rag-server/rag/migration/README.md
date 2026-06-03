# RAG Server Migration Services

This directory contains migration services for the RAG server vector database.

## Overview

The RAG server supports two migration paths:

1. **ChromaDB → Qdrant** - Migrate from legacy ChromaDB to Qdrant
2. **Embedded Qdrant → Standalone Qdrant** - Move to standalone server with on-disk storage

## Files

### Migration Services

- `migration_service.py` - ChromaDB → Qdrant migration orchestration
- `chroma_exporter.py` - Exports data from ChromaDB collections
- `qdrant_importer.py` - Imports data into Qdrant collections
- `qdrant_to_qdrant_migration.py` - Embedded → Standalone Qdrant migration

### Controllers

- `../controllers/migration_controller.py` - FastAPI endpoints for migrations

## Quick Start

### ChromaDB → Qdrant Migration

```bash
# Validate
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/validate

# Start migration
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/start \
  -H "Content-Type: application/json" \
  -d '{"batch_size": 100, "force": false}'

# Check status
curl http://localhost:9999/api/migrate/chroma-to-qdrant/status
```

### Embedded → Standalone Qdrant Migration

```bash
# Validate
curl -X POST http://localhost:9999/api/migrate/qdrant-to-qdrant/validate

# Start migration (runs in background)
curl -X POST "http://localhost:9999/api/migrate/qdrant-to-qdrant/start?batch_size=25"

# Check status
curl http://localhost:9999/api/migrate/qdrant-to-qdrant/status

# Get statistics
curl http://localhost:9999/api/migrate/qdrant-to-qdrant/stats
```

## API Endpoints

All migration endpoints are prefixed with `/api/migrate`:

### ChromaDB → Qdrant
- `POST /chroma-to-qdrant/validate` - Validate migration prerequisites
- `POST /chroma-to-qdrant/start` - Start migration
- `GET /chroma-to-qdrant/status` - Get migration status
- `GET /chroma-to-qdrant/stats` - Get database statistics
- `POST /chroma-to-qdrant/cleanup` - Cleanup ChromaDB after migration
- `POST /chroma-to-qdrant/rollback` - Rollback migration

### Embedded → Standalone Qdrant
- `POST /qdrant-to-qdrant/validate` - Validate migration prerequisites
- `POST /qdrant-to-qdrant/start` - Start migration (background)
- `GET /qdrant-to-qdrant/status` - Get migration status
- `GET /qdrant-to-qdrant/stats` - Get source and target statistics

## Features

### ChromaDB → Qdrant Migration
- Progressive migration (no full backup required)
- Batch processing to manage memory
- Automatic retry on failures
- Collection-level progress tracking
- Rollback support

### Embedded → Standalone Qdrant Migration
- Background execution (non-blocking)
- Conservative batch sizes (prevents pod restarts)
- Automatic retry with exponential backoff
- On-disk storage optimization (vectors + HNSW)
- Metadata preservation
- Point count verification

## Documentation

For detailed migration guides, see:

- **[Qdrant Migration Guide](../../docs/QDRANT_MIGRATION_GUIDE.md)** - Complete migration documentation
- **[Standalone Qdrant Deployment](../../docs/STANDALONE_QDRANT_DEPLOYMENT.md)** - Deployment guide

## Architecture

```
ChromaDB (Legacy)
    ↓
    ├─→ chroma_exporter.py → Exports collections
    │
    └─→ migration_service.py → Orchestrates migration
         ↓
         qdrant_importer.py → Imports to Qdrant
              ↓
         Embedded Qdrant (Local)
              ↓
         qdrant_to_qdrant_migration.py → Migrates to server
              ↓
         Standalone Qdrant (Server + On-disk)
```

## Error Handling

Both migration services include:
- Automatic retry with exponential backoff
- Detailed error logging
- Skip already-migrated collections
- Continue on individual collection failures
- Status tracking for monitoring

## Performance

### ChromaDB → Qdrant
- Batch size: 100 documents (configurable)
- Memory-efficient using bulk SQL queries
- No full database lock required

### Embedded → Standalone Qdrant
- Batch size: 25 points (conservative, configurable)
- 2-second delay between batches
- 3-second delay between collections
- 5-minute client timeout
- Background thread execution

## Configuration

### Environment Variables

**QDRANT_URL** - Standalone Qdrant server URL
- If not set: Uses embedded Qdrant (local storage)
- If set: Connects to standalone Qdrant server
- Example: `http://nudgebee-qdrant-server:6333`

## Monitoring

Check migration progress:
```bash
# Get status
curl http://localhost:9999/api/migrate/qdrant-to-qdrant/status

# Get stats
curl http://localhost:9999/api/migrate/qdrant-to-qdrant/stats

# View logs
kubectl logs -n nudgebee -l app=rag-server --tail=100 -f
```

## Troubleshooting

### Migration Failed

1. Check status endpoint for error details
2. Review RAG server logs
3. Verify source and target connectivity
4. Retry with `force=true` if needed

### Pod Restarts During Migration

Already fixed in current implementation:
- Background thread execution
- Conservative batch sizes
- Proper delays between operations
- Health endpoint remains responsive

### Metadata Missing

Already fixed - uses correct Qdrant 1.16.0+ metadata path

## Development

### Running Tests

```bash
# Install dependencies
poetry install

# Run tests
poetry run pytest rag/migration/
```

### Adding New Migration

1. Create migration service in this directory
2. Add API endpoints in `controllers/migration_controller.py`
3. Add documentation in `docs/`
4. Update this README

## See Also

- [Main README](../../README.md)
- [Qdrant Documentation](https://qdrant.tech/documentation/)
- [FastAPI Documentation](https://fastapi.tiangolo.com/)
