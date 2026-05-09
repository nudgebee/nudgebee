# RAG Server API - Postman Collection Guide

## 📦 Import the Collection

1. Open Postman
2. Click **Import** button (top left)
3. Select the file: `RAG-Server-API.postman_collection.json`
4. Click **Import**

## 🔧 Configure Variables

After importing, configure these collection variables:

| Variable | Default Value | Description |
|----------|---------------|-------------|
| `base_url` | `http://localhost:9999` | RAG server URL |
| `account_id` | `123` | Default account ID for testing |
| `tenant_id` | `tenant_001` | Default tenant ID for testing |

**To Edit Variables:**
1. Right-click on collection → **Edit**
2. Go to **Variables** tab
3. Update **Current Value** column
4. Click **Save**

## 📚 API Endpoints Overview

### 1. Health (1 endpoint)
- `GET /health` - Health check

### 2. Document Management (3 endpoints)
- `POST /load_docs` - Load documents for modules
- `POST /load_account_module_docs` - Load account-specific documents
- `DELETE /delete_collections` - Delete all collections

### 3. Search (3 endpoints)
- `POST /get_matching_doc` - General search with metadata filtering
- `POST /get_matching_doc` (with filter) - Search with metadata filter example
- `POST /get_prometheus_matching_doc` - Prometheus-specific search

### 4. Knowledge Base (5 endpoints)
- `POST /kb/create` - Create account-based KB
- `DELETE /kb/{account_id}/{kb_id}` - Delete account KB
- `POST /knowledge` - Create tenant KB
- `GET /knowledge` - List all tenant KBs
- `DELETE /knowledge/{tenant_id}` - Delete tenant KB

### 5. Cache Management (2 endpoints)
- `GET /cache/stats` - View cache statistics
- `POST /cache/clear` - Clear collection index cache

### 6. Collection Management (10 endpoints)
- `GET /collections` - List all collections
- `GET /collections/{name}/query` - Query collection
- `GET /collections/{name}/download` - Download collection data
- `GET /collections/{name}` - Get collection info
- `DELETE /collections` - Delete all collections (admin)
- `DELETE /collections/{name}` - Delete specific collection
- `DELETE /collections/module/{module}` - Delete module collections
- `POST /admin/configure-ondisk` - Configure on-disk storage
- `POST /admin/export-qdrant-collections` - Export collections
- `POST /admin/import-qdrant-collections` - Import collections

### 7. Migration (8 endpoints)
- `POST /qdrant-to-qdrant/validate` - Validate migration
- `POST /qdrant-to-qdrant/start` - Start migration
- `GET /qdrant-to-qdrant/status` - Get migration status
- `GET /qdrant-to-qdrant/stats` - Get migration stats
- `POST /rag-collections/get-mappings` - Get collection mappings
- `POST /rag-collections/rename` - Rename collections
- `GET /rag-collections/verify` - Verify collections
- `POST /rag-collections/cleanup` - Cleanup invalid collections

## 🚀 Quick Start Examples

### 1. Check Server Health
```
GET {{base_url}}/health
```

### 2. Load Prometheus Documents
```
POST {{base_url}}/load_docs
{
  "module": "prometheus",
  "force": false,
  "account_id": ["123"]
}
```

### 3. Search Documents
```
POST {{base_url}}/get_matching_doc
{
  "query": "What is CPU usage?",
  "k": 3,
  "account_id": "123",
  "module": "prometheus",
  "conversation_id": "conv_001",
  "message_id": "msg_001"
}
```

### 4. Search with Metadata Filter
```
POST {{base_url}}/get_matching_doc
{
  "query": "high priority issues",
  "k": 5,
  "account_id": "123",
  "module": "events",
  "metadata_filter": {
    "priority": "high"
  }
}
```

### 5. Create Knowledge Base
```
POST {{base_url}}/kb/create
{
  "account_id": "123",
  "kb_id": "kb_001",
  "data": "Knowledge base content here\nLine 2\nLine 3",
  "format": "text"
}
```

## 📝 Supported Modules

| Module | Description |
|--------|-------------|
| `prometheus` | Prometheus metrics and queries |
| `loki` | Loki log queries |
| `elastic_search` | ElasticSearch queries |
| `events` | Event documents |
| `recommendations` | Recommendation documents |
| `planner` | Planning documents |
| `rabbitmq` | RabbitMQ documentation |
| `aws` | AWS CLI documentation |
| `kubectl` | Kubernetes kubectl documentation |
| `postgresql` | PostgreSQL documentation |
| `mysql` | MySQL documentation |
| `github` | GitHub documentation |
| `docs` | General documentation |

## 🎯 Supported Data Formats

| Format | Description | Example |
|--------|-------------|---------|
| `text` | Plain text (line-by-line) | Multi-line text |
| `json` | JSON array | `[{"key": "value"}]` |
| `xml` | XML documents | `<root>...</root>` |
| `csv` | Comma-separated values | CSV with headers |

## 🔍 Metadata Filtering Examples

### Simple Field Match
```json
{
  "metadata_filter": {
    "source": "docs.example.com"
  }
}
```

### Multiple Conditions (AND)
```json
{
  "metadata_filter": {
    "must": [
      {"field": "priority", "values": ["high", "critical"]},
      {"field": "status", "values": ["open"]}
    ]
  }
}
```

### Range Filter
```json
{
  "metadata_filter": {
    "must": [
      {"field": "timestamp", "gte": 1234567890}
    ]
  }
}
```

## ⚠️ Important Notes

1. **Token Tracking**: Set `track_token_usage: false` if you don't want to track usage (skips conversation_id/message_id validation)

2. **Batch Operations**: Use `force: true` to reload documents even if they already exist

3. **Migration Dry Run**: Always use `dry_run: true` first when renaming or cleaning up collections

4. **Admin Endpoints**: Some endpoints (export/import/configure-ondisk) are for administrative use only

5. **Collection Names**: Follow naming convention: `{account_id}_{module}` or `{module}_global`

## 🔗 Related Documentation

- See `docs/METADATA_FILTERING.md` for detailed metadata filter examples
- See `docs/SEARCH_OPTIMIZATIONS.md` for search performance tips
- See `docs/DOCUMENT_LOADING_OPTIMIZATIONS.md` for bulk loading best practices

## 💡 Tips

1. **Use Variables**: Create environment-specific variables for different deployments
2. **Save Responses**: Use Postman's "Save Response" feature for testing
3. **Test Scripts**: Add test scripts to validate API responses
4. **Environments**: Create separate environments for dev/test/prod

---

**Total Endpoints**: 32 APIs across 7 categories
**Collection Version**: 1.0
**Last Updated**: January 2026
