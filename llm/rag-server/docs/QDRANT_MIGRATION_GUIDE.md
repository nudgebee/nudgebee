# Qdrant Migration Guide

## Overview

This guide covers two migration paths for the RAG server:

1. **ChromaDB → Qdrant** (embedded or standalone)
2. **Embedded Qdrant → Standalone Qdrant**

## Migration Architecture

```
┌─────────────┐
│  ChromaDB   │
│  (Legacy)   │
└──────┬──────┘
       │
       │ Migration 1: ChromaDB → Qdrant
       ↓
┌──────────────────┐
│ Embedded Qdrant  │
│ (Local Storage)  │
└──────┬───────────┘
       │
       │ Migration 2: Embedded → Standalone
       ↓
┌──────────────────────┐
│ Standalone Qdrant    │
│ (Server Mode)        │
│ + On-disk storage    │
│ + HNSW on-disk       │
└──────────────────────┘
```

## Migration 1: ChromaDB → Qdrant

### Purpose
Migrate from ChromaDB to Qdrant (embedded or standalone mode).

### API Endpoints

**Base URL:** `http://localhost:9999/api/migrate`

#### 1. Validate Migration
```bash
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/validate
```

**Response:**
```json
{
  "success": true,
  "validation": {
    "valid": true,
    "checks": [
      {"check": "chromadb", "status": "ok", "collections": 36},
      {"check": "qdrant", "status": "ok", "collections": 0}
    ]
  }
}
```

#### 2. Start Migration
```bash
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/start \
  -H "Content-Type: application/json" \
  -d '{
    "dry_run": false,
    "batch_size": 100,
    "force": false,
    "skip_backup": true
  }'
```

**Parameters:**
- `dry_run`: Validate only without migrating (default: false)
- `batch_size`: Documents per batch (default: 100)
- `force`: Re-migrate existing collections (default: false)
- `skip_backup`: Skip backup creation (default: true, recommended)

**Response:**
```json
{
  "success": true,
  "message": "Migration started",
  "dry_run": false,
  "force": false
}
```

#### 3. Check Status
```bash
curl http://localhost:9999/api/migrate/chroma-to-qdrant/status
```

**Response:**
```json
{
  "success": true,
  "status": {
    "status": "in_progress",
    "progress": 45,
    "current_collection": "collection_name",
    "total_collections": 36,
    "migrated": 16,
    "failed": 0
  }
}
```

#### 4. Get Statistics
```bash
curl http://localhost:9999/api/migrate/chroma-to-qdrant/stats
```

## Migration 2: Embedded Qdrant → Standalone Qdrant

### Purpose
Migrate from local embedded Qdrant to standalone Qdrant server with on-disk storage optimization.

**Benefits:**
- Memory reduction: ~2GB → ~1.2GB total (500MB RAG + 700MB Qdrant)
- Better resource isolation
- Easier scaling and management
- Full on-disk storage support (vectors + HNSW index)

### Prerequisites

1. **Deploy Standalone Qdrant Server:**
   ```bash
   cd /path/to/nudgebee/deploy/kubernetes/nudgebee-qdrant-server

   # For dev
   helm upgrade nudgebee-qdrant-server -f values-dev.yaml . --install --namespace nudgebee

   # For test
   helm upgrade nudgebee-qdrant-server -f values-test.yaml . --install --namespace nudgebee

   # For prod
   helm upgrade nudgebee-qdrant-server -f values-prod.yaml . --install --namespace nudgebee
   ```

2. **Update RAG Server Configuration:**

   Set `QDRANT_URL` in RAG server values files (already configured):
   ```yaml
   env:
     QDRANT_URL: "http://nudgebee-qdrant-server:6333"
   ```

3. **Deploy Updated RAG Server:**
   ```bash
   # Deploy with QDRANT_URL configured
   helm upgrade rag-server -f values-{env}.yaml . --install --namespace nudgebee
   ```

### API Endpoints

**Base URL:** `http://localhost:9999/api/migrate`

#### 1. Validate Migration
```bash
curl -X POST http://localhost:9999/api/migrate/qdrant-to-qdrant/validate
```

**Response:**
```json
{
  "success": true,
  "validation": {
    "valid": true,
    "checks": [
      {
        "check": "embedded_qdrant",
        "status": "ok",
        "collections": 36
      },
      {
        "check": "standalone_qdrant",
        "status": "ok",
        "collections": 0
      },
      {
        "check": "qdrant_url",
        "status": "ok",
        "url": "http://nudgebee-qdrant-server:6333"
      }
    ],
    "errors": []
  }
}
```

#### 2. Start Migration (Background)
```bash
curl -X POST "http://localhost:9999/api/migrate/qdrant-to-qdrant/start?batch_size=25&force=false"
```

**Query Parameters:**
- `batch_size`: Points per batch (default: 25, conservative to prevent pod restarts)
- `force`: Force re-migration of existing collections (default: false)

**Response:**
```json
{
  "success": true,
  "message": "Qdrant→Qdrant migration started in background",
  "batch_size": 25,
  "force": false
}
```

**Note:** Migration runs in a background thread to keep the health endpoint responsive and prevent Kubernetes pod restarts due to liveness probe failures.

#### 3. Check Status
```bash
curl http://localhost:9999/api/migrate/qdrant-to-qdrant/status
```

**Response:**
```json
{
  "success": true,
  "status": {
    "status": "in_progress",
    "progress": {
      "total_collections": 36,
      "migrated": 20,
      "skipped": 0,
      "failed": 0,
      "details": [...]
    }
  }
}
```

**Status Values:**
- `not_started` - No migration running
- `in_progress` - Migration currently running
- `completed` - Migration finished successfully
- `failed` - Migration failed (check error field)

#### 4. Get Statistics
```bash
curl http://localhost:9999/api/migrate/qdrant-to-qdrant/stats
```

**Response:**
```json
{
  "success": true,
  "source": {
    "total_collections": 36,
    "collections": [
      {"name": "collection1", "points_count": 1234}
    ]
  },
  "target": {
    "total_collections": 36,
    "collections": [
      {"name": "collection1", "points_count": 1234}
    ]
  }
}
```

### Migration Process Details

**What happens during migration:**

1. **For each collection:**
   - Reads full collection info from embedded Qdrant
   - Extracts vector config (size, distance metric)
   - Extracts metadata
   - Creates collection in standalone Qdrant with:
     - `vectors.on_disk = True` (low memory usage)
     - `hnsw_config.on_disk = True` (HNSW index on disk)
   - Migrates data in small batches (25 points per batch)
   - 2-second delay between batches
   - Verifies point counts match

2. **Between collections:**
   - 3-second delay to prevent server overload

3. **Error handling:**
   - Automatic retry with exponential backoff (3 attempts)
   - Skips already-migrated collections
   - Continues with remaining collections if one fails

### Performance Characteristics

**Conservative settings to prevent pod restarts:**
- Batch size: 25 points (vs default 100)
- Delay between batches: 2 seconds
- Delay between collections: 3 seconds
- Client timeout: 300 seconds (5 minutes)
- Background thread execution (non-blocking)

**Estimated time:**
- ~10-15 collections/hour (conservative)
- For 36 collections: ~2-3 hours
- Slower but stable, no pod restarts

## Troubleshooting

### Migration Stuck or Failed

1. **Check status:**
   ```bash
   curl http://localhost:9999/api/migrate/qdrant-to-qdrant/status
   ```

2. **Check RAG server logs:**
   ```bash
   kubectl logs -n nudgebee -l app=rag-server --tail=100 -f
   ```

3. **Check Qdrant server logs:**
   ```bash
   kubectl logs -n nudgebee nudgebee-qdrant-server-0 --tail=100 -f
   ```

### Pod Keeps Restarting During Migration

**Issue:** Liveness probe failures due to high load

**Solution:** Already fixed in current implementation:
- Migration runs in background thread
- Health endpoint always responsive
- Conservative batch sizes and delays
- Proper timeout settings

### Metadata Missing After Migration

**Issue:** Collection metadata empty after migration

**Solution:** Already fixed - metadata is now extracted from `collection_info.config.metadata` (Qdrant 1.16.0+)

### Point Count Mismatch

**Issue:** Target has fewer points than source

**Check:**
1. Review migration logs for errors
2. Retry migration with `force=true`:
   ```bash
   curl -X POST "http://localhost:9999/api/migrate/qdrant-to-qdrant/start?force=true"
   ```

## Cleanup (Optional)

After successful migration to standalone Qdrant, you can clean up embedded Qdrant storage:

**⚠️ WARNING:** Only do this after verifying all data is correctly migrated!

```bash
# Exec into RAG server pod
kubectl exec -n nudgebee -it rag-server-0 -- bash

# Check current size
du -sh /nudgebee/rag-server/db/storage

# Remove embedded Qdrant data (BE CAREFUL!)
# rm -rf /nudgebee/rag-server/db/storage/*
```

## Monitoring

### Health Checks

**RAG Server:**
```bash
curl http://localhost:9999/health
```

**Qdrant Server:**
```bash
curl http://nudgebee-qdrant-server:6333/
```

### Memory Usage

**Before migration (embedded):**
- RAG server: ~2GB

**After migration (standalone):**
- RAG server: ~500MB-700MB
- Qdrant server: ~700MB-1GB
- **Total: ~1.2-1.7GB** (reduction achieved!)

## Configuration Reference

### Environment Variables

**RAG Server:**
- `QDRANT_URL` - Standalone Qdrant server URL (e.g., `http://nudgebee-qdrant-server:6333`)
  - If not set: Uses embedded Qdrant (local storage)
  - If set: Connects to standalone Qdrant server

**Qdrant Server (Helm values):**
```yaml
resources:
  limits:
    memory: 1Gi
  requests:
    memory: 700Mi

persistence:
  enabled: true
  size: 50Gi
  mountPath: /qdrant/storage

config:
  storage:
    on_disk_payload: true
    optimizers:
      indexing_threshold_kb: 20000
      memmap_threshold_kb: 10000
```

## See Also

- [Standalone Qdrant Deployment Guide](./STANDALONE_QDRANT_DEPLOYMENT.md)
- [Migration Service README](../rag/migration/README.md)
- [Qdrant Documentation](https://qdrant.tech/documentation/)
