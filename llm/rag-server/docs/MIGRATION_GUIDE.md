# RAG Server Migration: ChromaDB → Qdrant

## Quick Start

### Prerequisites

1. **Install dependencies:**
   ```bash
   cd /Users/hsundar/Nudgebee/nudgebee/llm/rag-server
   poetry install
   ```

2. **Ensure RAG server is running:**
   ```bash
   poetry run python server.py
   # Or if running via gunicorn:
   poetry run gunicorn -c gunicorn.conf.py server:server
   ```

### Migration Options

#### Option 1: REST API (Recommended for Production)

```bash
# 1. Validate migration
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/validate

# 2. Start migration
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/start \
  -H "Content-Type: application/json" \
  -d '{"batch_size": 100}'

# 3. Monitor progress
curl http://localhost:9999/api/migrate/chroma-to-qdrant/status

# 4. After testing, cleanup old files
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/cleanup \
  -H "Content-Type: application/json" \
  -d '{"confirm": true}'
```

#### Option 2: Test Script (Recommended for Development)

```bash
cd /Users/hsundar/Nudgebee/nudgebee/llm/rag-server
poetry run python rag/migration/test_migration.py
```

Interactive script will:
- Validate prerequisites
- Ask for confirmation
- Run migration
- Show results and next steps

## What Happens During Migration

### 1. Validation Phase
- ✓ Checks ChromaDB path exists
- ✓ Counts collections and documents
- ✓ Verifies disk space (needs ~2.5x current size)
- ✓ Estimates migration time

### 2. Backup Phase
- Creates timestamped backup in `/tmp/chroma_backup_YYYYMMDD_HHMMSS/`
- Copies all ChromaDB files
- Preserves backup even after cleanup

### 3. Export Phase
- Exports all collections from ChromaDB
- Includes documents, embeddings, and metadata
- Processes in batches for memory efficiency

### 4. Import Phase
- Creates Qdrant collections in `/nudgebee/rag-server/db/qdrant/`
- Imports all documents with embeddings
- Preserves metadata and collection structure

### 5. Verification Phase
- Compares document counts
- Validates all collections imported
- Reports any discrepancies

### 6. Cleanup Phase (Manual)
- Removes ChromaDB files (after user confirmation)
- Keeps Qdrant directory
- Preserves backup in `/tmp`

## Storage Layout

### Before Migration
```
/nudgebee/rag-server/db/
├── chroma.sqlite3          # ChromaDB metadata
└── [uuid-segments]/        # ChromaDB embeddings
```

### After Migration (Before Cleanup)
```
/nudgebee/rag-server/db/
├── chroma.sqlite3          # ChromaDB (still present)
├── [uuid-segments]/        # ChromaDB (still present)
├── collection/             # NEW: Qdrant collection data
├── storage/                # NEW: Qdrant storage
├── meta.json               # NEW: Qdrant metadata
└── .lock                   # NEW: Qdrant lock file

/tmp/chroma_backup_20250118_143022/  # Backup
└── [all ChromaDB files]
```

### After Cleanup
```
/nudgebee/rag-server/db/
├── collection/             # Only Qdrant files remain
├── storage/
├── meta.json
└── .lock
```

## Key Features

✅ **Safe Migration**
- Automatic backup before migration
- Rollback capability
- No data loss risk

✅ **Same Directory**
- Qdrant uses same base path as ChromaDB
- Just adds `/qdrant` subdirectory
- Easy cleanup after migration

✅ **Batch Processing**
- Memory-efficient
- Configurable batch size
- Progress tracking

✅ **Verification**
- Document count validation
- Collection integrity checks
- Metadata preservation

✅ **Error Handling**
- Detailed logging
- Error recovery
- Partial migration support

## API Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/migrate/chroma-to-qdrant/validate` | POST | Pre-migration validation |
| `/api/migrate/chroma-to-qdrant/start` | POST | Start migration |
| `/api/migrate/chroma-to-qdrant/status` | GET | Check progress |
| `/api/migrate/chroma-to-qdrant/stats` | GET | Compare databases |
| `/api/migrate/chroma-to-qdrant/cleanup` | POST | Remove ChromaDB files |
| `/api/migrate/chroma-to-qdrant/rollback` | POST | Restore from backup |

## Rollback Procedure

If you need to revert to ChromaDB:

```bash
# Via API
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/rollback

# Via script
poetry run python rag/migration/test_migration.py --rollback
```

This will:
1. Remove `/nudgebee/rag-server/db/qdrant/`
2. Restore ChromaDB from `/tmp/chroma_backup_*/`
3. Allow you to retry migration

## Testing After Migration

Before cleanup, verify Qdrant works:

```bash
# Test a query
curl -X POST http://localhost:9999/api/rag/search \
  -H "Content-Type: application/json" \
  -d '{
    "query": "kubernetes pod restart",
    "module": "prometheus",
    "account_id": "your-account-id",
    "k": 5
  }'

# Compare stats
curl http://localhost:9999/api/migrate/chroma-to-qdrant/stats | jq
```

## Performance Notes

- **Speed**: ~1000 documents/minute (varies by size)
- **Memory**: Peak ~2-3x normal usage during migration
- **Disk**: Needs ~2.5x current ChromaDB size
- **Downtime**: Migration runs in background, server stays up

## Files Added

```
llm/rag-server/
├── pyproject.toml                          # Updated: Added qdrant dependencies
├── rag/
│   ├── qdrant_client.py                   # NEW: Qdrant client wrapper
│   └── migration/
│       ├── __init__.py                     # NEW
│       ├── chroma_exporter.py             # NEW: Export utility
│       ├── qdrant_importer.py             # NEW: Import utility
│       ├── migration_service.py           # NEW: Orchestration
│       ├── test_migration.py              # NEW: Test script
│       └── README.md                       # NEW: Detailed guide
└── controllers/
    └── migration_controller.py            # NEW: REST API
```

## Troubleshooting

### "Insufficient disk space"
- Free up space or change `CHROMA_PERSISTENT_PATH`
- Minimum: 2.5x current ChromaDB size

### "Migration already in progress"
- Wait for completion or restart RAG server
- Check status with `/api/migrate/chroma-to-qdrant/status`

### "Collection import failed"
- Check logs for specific error
- Verify embeddings exist in ChromaDB
- May need to retry individual collection

### "Verification failed"
- Check document count mismatch
- Review logs for import errors
- Safe to rollback and retry

## Next Steps After Migration

1. **Test thoroughly** - Verify all RAG queries work correctly
2. **Monitor performance** - Check search latency and accuracy
3. **Update code** (future) - Eventually switch to Qdrant client directly
4. **Cleanup** - After confirming success, remove ChromaDB files
5. **Archive backup** - Move `/tmp/chroma_backup_*` to permanent storage

## Support

For detailed documentation, see:
- `rag/migration/README.md` - Complete migration guide
- Server logs - Check `/var/log/rag-server/` or console output
- Test script - Run `rag/migration/test_migration.py` for interactive testing

## Implementation Details

### Dependencies Added
- `qdrant-client==1.12.1` - Python client for Qdrant
- `langchain-qdrant==0.2.0` - LangChain integration

### Configuration
Uses existing `CHROMA_PERSISTENT_PATH` environment variable.
Qdrant storage: **Same directory as ChromaDB** (not a subdirectory!)

**Important:** Qdrant files are stored directly in `CHROMA_PERSISTENT_PATH`:
- `collection/` - Qdrant collection data
- `storage/` - Qdrant storage files
- `meta.json` - Qdrant metadata
- `.lock` - Qdrant lock file

### Backward Compatibility
- Migration is non-destructive until cleanup
- Can rollback at any time before cleanup
- Backup preserved even after cleanup
- Original ChromaDB code unchanged (no code migration required yet)
