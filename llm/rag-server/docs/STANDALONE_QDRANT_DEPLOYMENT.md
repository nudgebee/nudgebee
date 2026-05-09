# Standalone Qdrant Server Deployment Guide

## Overview

This guide walks through migrating from embedded Qdrant to standalone Qdrant server mode to enable full on-disk storage support (both vectors and HNSW index).

## Why Standalone Mode?

**Problem:** In embedded Qdrant mode, `hnsw_config.on_disk=True` doesn't work - the setting remains `None`.

**Solution:** Deploy standalone Qdrant server where both `vectors.on_disk=True` and `hnsw_config.on_disk=True` work properly.

**Benefits:**
- Full on-disk storage support (vectors + HNSW index)
- Better memory efficiency (~40% reduction expected)
- Scalability for production workloads
- Separate resource management for vector database

## Architecture

**Before (Embedded Mode):**
```
rag-server pod
├── RAG Server (Python)
└── Embedded Qdrant (in-process)
    └── PersistentVolume (50Gi)
```

**After (Standalone Mode):**
```
rag-server pod                    nudgebee-qdrant-server pod
├── RAG Server (Python)           └── Qdrant Server
└── Connects via HTTP    ─────►      └── PersistentVolume (50Gi)
```

## Deployment Steps

### Overview of Migration Paths

**Development (already using embedded Qdrant):**
1. Deploy standalone Qdrant server
2. Export from embedded Qdrant
3. Update rag-server to use standalone Qdrant
4. Import to standalone Qdrant (already creates with on_disk=True)
5. Verify on_disk configuration

**Test/Prod (still using ChromaDB):**
1. Deploy standalone Qdrant server
2. Update rag-server to use standalone Qdrant
3. Run ChromaDB→Qdrant migration (automatically creates with on_disk=True)
4. Verify on_disk configuration

---

## Migration for Development (Embedded Qdrant → Standalone Qdrant)

### Step 1: Deploy Standalone Qdrant Server

```bash
cd /path/to/nudgebee/deploy/kubernetes/nudgebee-qdrant-server

helm upgrade nudgebee-qdrant-server \
  -f values-dev.yaml \
  . \
  --install \
  --namespace nudgebee \
  --create-namespace

# Verify deployment
kubectl get pods -n nudgebee | grep nudgebee-qdrant-server
kubectl logs -n nudgebee -l app=nudgebee-qdrant-server --tail=50
```

Expected:
```
NAME                   READY   STATUS    RESTARTS   AGE
nudgebee-qdrant-server-0     1/1     Running   0          30s
```

### Step 2: Export from Embedded Qdrant

**BEFORE upgrading rag-server**, export all collections:

```bash
# Port-forward to current rag-server (still using embedded Qdrant)
kubectl port-forward -n nudgebee svc/rag-server 9999:9999 &

# Export all collections
curl -X POST http://localhost:9999/admin/export-qdrant-collections \
  -H "Content-Type: application/json" \
  -o qdrant_collections_backup.json

# Verify export
ls -lh qdrant_collections_backup.json
cat qdrant_collections_backup.json | jq '.data.exported_collections'
```

### Step 3: Deploy Updated RAG Server

The configuration has already been updated to use standalone Qdrant:

```bash
cd /path/to/nudgebee/deploy/kubernetes/rag-server

helm upgrade rag-server \
  -f values-dev.yaml \
  . \
  --install \
  --namespace nudgebee

# Wait for rag-server to be ready
kubectl wait --for=condition=ready pod -l app=rag-server-dev -n nudgebee --timeout=120s

# Verify it connects to standalone Qdrant
kubectl logs -n nudgebee -l app=rag-server-dev --tail=50 | grep -i qdrant
```

Expected logs:
```
Using Qdrant server mode: http://nudgebee-qdrant-server:6333
Connected to Qdrant server at: http://nudgebee-qdrant-server:6333
```

### Step 4: Import to Standalone Qdrant

Import collections with on_disk=True (automatic):

```bash
# Port-forward to updated rag-server
kubectl port-forward -n nudgebee svc/rag-server 9999:9999 &

# Import collections
curl -X POST http://localhost:9999/admin/import-qdrant-collections \
  -H "Content-Type: application/json" \
  -d @qdrant_collections_backup.json

# Monitor import progress
kubectl logs -n nudgebee -l app=rag-server-dev -f | grep "Importing collection"
```

Expected response:
```json
{
  "data": {
    "total_collections": 36,
    "imported_collections": 36,
    "failed_collections": 0,
    "details": [...]
  }
}
```

### Step 5: Verify On-Disk Configuration

Verify that both vectors and HNSW are on disk:

```bash
# Check first collection
curl http://localhost:9999/collections | jq '.data[0].name'
COLLECTION_NAME=$(curl -s http://localhost:9999/collections | jq -r '.data[0].name')

curl http://localhost:9999/collections/$COLLECTION_NAME | \
  jq '.data.config | {vectors_on_disk: .params.vectors.on_disk, hnsw_on_disk: .hnsw_config.on_disk}'
```

Expected:
```json
{
  "vectors_on_disk": true,
  "hnsw_on_disk": true
}
```

### Step 6: Verify Memory Reduction

```bash
# Check memory usage
kubectl top pod -n nudgebee | grep -E "rag-server|qdrant"
```

Expected: Combined memory ~40% lower than previous embedded mode.

---

## Migration for Test/Prod (ChromaDB → Standalone Qdrant)

### Step 1: Deploy Standalone Qdrant Server

#### Test:
```bash
cd /path/to/nudgebee/deploy/kubernetes/nudgebee-qdrant-server

helm upgrade nudgebee-qdrant-server \
  -f values-test.yaml \
  . \
  --install \
  --namespace nudgebee \
  --create-namespace
```

#### Production:
```bash
helm upgrade nudgebee-qdrant-server \
  -f values-prod.yaml \
  . \
  --install \
  --namespace nudgebee \
  --create-namespace
```

**Verify deployment:**
```bash
kubectl get pods -n nudgebee | grep nudgebee-qdrant-server
kubectl logs -n nudgebee -l app=nudgebee-qdrant-server --tail=50
```

### Step 2: Deploy Updated RAG Server

#### Test:
```bash
cd /path/to/nudgebee/deploy/kubernetes/rag-server

helm upgrade rag-server \
  -f values-test.yaml \
  . \
  --install \
  --namespace nudgebee
```

#### Production:
```bash
helm upgrade rag-server \
  -f values-prod.yaml \
  . \
  --install \
  --namespace nudgebee
```

**Verify connection:**
```bash
kubectl logs -n nudgebee -l app=rag-server-test --tail=50 | grep -i qdrant  # or app=rag-server-prod
```

### Step 3: Run ChromaDB to Qdrant Migration

The migration automatically creates collections with on_disk=True:

```bash
# Port-forward to rag-server
kubectl port-forward -n nudgebee svc/rag-server 9999:9999 &

# Validate migration prerequisites
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/validate

# Start migration
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/start \
  -H "Content-Type: application/json" \
  -d '{"dry_run": false, "batch_size": 100, "skip_backup": true}'

# Monitor progress
watch -n 5 'curl -s http://localhost:9999/api/migrate/chroma-to-qdrant/status | jq'
```

Expected final status:
```json
{
  "success": true,
  "status": {
    "status": "completed",
    "progress": 100,
    "collections_migrated": 36,
    ...
  }
}
```

### Step 4: Verify On-Disk Configuration

Verify that collections were created with on_disk=True:

```bash
# Get collection stats
curl http://localhost:9999/api/migrate/chroma-to-qdrant/stats | jq '.qdrant'
```

Check a specific collection:
```bash
COLLECTION_NAME=$(curl -s http://localhost:9999/collections | jq -r '.data[0].name')

curl http://localhost:9999/collections/$COLLECTION_NAME | \
  jq '.data.config | {vectors_on_disk: .params.vectors.on_disk, hnsw_on_disk: .hnsw_config.on_disk}'
```

Expected:
```json
{
  "vectors_on_disk": true,
  "hnsw_on_disk": true
}
```

### Step 5: Cleanup ChromaDB (Optional)

After verifying migration success:

```bash
curl -X POST http://localhost:9999/api/migrate/chroma-to-qdrant/cleanup \
  -H "Content-Type: application/json" \
  -d '{"confirm": true}'
```

This will:
1. Check each collection's current configuration
2. Export all points (vectors + payloads)
3. Delete and recreate collection with `on_disk=True` for both vectors and HNSW
4. Re-import all points
5. Verify point counts

**Expected response:**
```json
{
  "data": {
    "total_collections": 36,
    "already_configured": 0,
    "newly_configured": 36,
    "failed": 0,
    "configured": 36,
    "details": [
      {
        "collection": "collection_name",
        "status": "newly_configured",
        "on_disk": true,
        "points_migrated": 1234
      },
      ...
    ]
  }
}
```

### Step 7: Verify Configuration

Check that both vectors and HNSW are on disk:

```bash
# Get collection info
curl -X GET http://localhost:9999/collections/{collection_name}
```

Look for:
```json
{
  "config": {
    "params": {
      "vectors": {
        "on_disk": true
      }
    },
    "hnsw_config": {
      "on_disk": true
    }
  }
}
```

**Verify memory usage:**
```bash
# Check rag-server memory
kubectl top pod -n nudgebee | grep rag-server

# Check nudgebee-qdrant-server memory
kubectl top pod -n nudgebee | grep nudgebee-qdrant-server
```

Expected: Combined memory should be lower than previous embedded mode (target: 40% reduction).

## Rollback Procedure

If you need to rollback to embedded mode:

### Step 1: Export data from standalone server
```bash
curl -X POST http://localhost:9999/admin/export-all-collections \
  -o collections_backup_server.json
```

### Step 2: Revert rag-server configuration

Comment out `QDRANT_URL` in values files:
```yaml
env:
  UVICORN_WORKERS: 1
  # QDRANT_URL: "http://nudgebee-qdrant-server:6333"
```

### Step 3: Redeploy rag-server
```bash
helm upgrade rag-server -f values-dev.yaml . --namespace nudgebee
```

### Step 4: Re-import data
```bash
curl -X POST http://localhost:9999/admin/import-collections \
  -H "Content-Type: application/json" \
  -d @collections_backup_server.json
```

### Step 5: Delete standalone server (optional)
```bash
helm delete nudgebee-qdrant-server --namespace nudgebee
kubectl delete pvc -n nudgebee data-nudgebee-qdrant-server-0
```

## Resource Configuration

### nudgebee-qdrant-server Resources

**Development:**
```yaml
resources:
  limits:
    cpu: 500m
    memory: 1Gi
  requests:
    cpu: 100m
    memory: 500Mi
persistence:
  size: 5Gi
```

**Test:**
```yaml
resources:
  limits:
    cpu: 1000m
    memory: 2Gi
  requests:
    cpu: 500m
    memory: 1Gi
persistence:
  size: 20Gi
```

**Production:**
```yaml
resources:
  limits:
    cpu: 2000m
    memory: 4Gi
  requests:
    cpu: 1000m
    memory: 2Gi
persistence:
  size: 50Gi
```

## Monitoring

### Health Checks

**Qdrant server health:**
```bash
kubectl port-forward -n nudgebee svc/nudgebee-qdrant-server 6333:6333
curl http://localhost:6333/healthz
```

**Collection count:**
```bash
curl http://localhost:9999/collections
```

### Logs

**Qdrant server logs:**
```bash
kubectl logs -n nudgebee -l app=nudgebee-qdrant-server -f
```

**RAG server logs:**
```bash
kubectl logs -n nudgebee -l app=rag-server-dev -f
```

## Troubleshooting

### Issue: rag-server can't connect to nudgebee-qdrant-server

**Check DNS resolution:**
```bash
kubectl exec -n nudgebee -it <rag-server-pod> -- nslookup nudgebee-qdrant-server
```

**Check service endpoints:**
```bash
kubectl get endpoints -n nudgebee nudgebee-qdrant-server
```

### Issue: Migration fails midway

**Check logs for specific collection:**
```bash
kubectl logs -n nudgebee -l app=rag-server-dev | grep "collection_name"
```

**Manually retry failed collections:**
The endpoint is idempotent - collections marked as "already_configured" will be skipped.

### Issue: Memory still high after migration

**Verify on_disk settings:**
```bash
curl http://localhost:9999/admin/configure-ondisk
```

Check that `already_configured` count equals `total_collections`.

**Force Qdrant to flush to disk:**
```bash
kubectl exec -n nudgebee nudgebee-qdrant-server-0 -- kill -HUP 1
```

## Performance Considerations

### Expected Performance

**Memory:**
- Before: ~2.2 GB (embedded, in RAM)
- After: ~1.2 GB total (~800 MB rag-server + ~400 MB qdrant-server)

**Query Latency:**
- Minimal increase (<10ms) due to network hop
- Disk I/O dependent on storage class

### Optimization Tips

1. **Use fast storage:** Ensure PersistentVolume uses SSD-backed storage class
2. **Adjust HNSW parameters:** Tune `m` and `ef_construct` for your use case
3. **Monitor disk IOPS:** On-disk mode requires good disk performance
4. **Scale horizontally:** For high load, consider Qdrant cluster mode

## References

- [Qdrant On-Disk Storage](https://qdrant.tech/documentation/guides/optimize/#2-high-precision-with-low-memory-usage)
- [Qdrant Server Configuration](https://qdrant.tech/documentation/guides/configuration/)
- [HNSW Index Documentation](https://qdrant.tech/documentation/concepts/indexing/#hnsw-index)
