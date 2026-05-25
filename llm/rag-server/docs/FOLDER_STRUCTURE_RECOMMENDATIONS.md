# RAG Server Folder Structure Analysis & Recommendations

> **Status: COMPLETED (January 2026)**
> All recommendations in this document have been implemented. The monolithic `loader.py` and `utils.py` have been split into modular packages, dead code has been removed, and all imports have been updated.

## Final Structure (Implemented)

```
llm/rag-server/
├── config/                           # ✅ Good - Configuration
│   └── __init__.py (42 lines)
│
├── controllers/                      # ⚠️ Mixed responsibilities
│   ├── cache_controller.py (129)    # ✅ Good - Single responsibility
│   ├── collection_controller.py (270) # ✅ Good
│   ├── health.py (9)                # ✅ Good
│   ├── knowledgebase_controller.py (195) # ✅ Good
│   ├── migration_controller.py (492) # ⚠️ Large - Could split
│   └── rag_controller.py (625)      # ❌ TOO LARGE - Multiple responsibilities
│
├── rag/
│   ├── core/
│   │   ├── documents/              # ⚠️ Mixed - Core logic + Module loaders
│   │   │   ├── collection.py (355)  # ✅ Good
│   │   │   ├── loader.py (1121)     # ❌ TOO LARGE - Multiple responsibilities
│   │   │   └── scraper.py (219)     # ✅ Good
│   │   │
│   │   ├── embeddings/             # ✅ Good structure
│   │   │   ├── generator.py        # ✅ Good
│   │   │   └── tracker.py          # ✅ Good
│   │   │
│   │   ├── llm/                    # ✅ Good structure
│   │   │   ├── prompts.py (306)    # ✅ Good
│   │   │   └── rag.py (369)        # ✅ Good
│   │   │
│   │   ├── monitoring/             # ✅ Good structure
│   │   │   ├── audit.py            # ✅ Good
│   │   │   ├── metrics.py          # ✅ Good
│   │   │   └── token_tracker.py    # ✅ Good
│   │   │
│   │   └── utils/                  # ⚠️ Generic name
│   │       └── db_query.py         # ✅ Specific - Good
│   │
│   ├── migration/                   # ✅ Good structure
│   │   ├── qdrant_exporter.py
│   │   ├── qdrant_importer.py
│   │   └── qdrant_to_qdrant_migration.py
│   │
│   ├── qdrant/                     # ✅ Good structure
│   │   └── client.py               # ✅ Good
│   │
│   ├── search/                     # ✅ Good structure (NEW!)
│   │   ├── cache.py (383)          # ✅ Good
│   │   ├── filters.py (274)        # ✅ Good
│   │   └── search_logic.py (164)   # ✅ Good
│   │
│   ├── exceptions.py               # ✅ Good
│   ├── migration_lock.py           # ⚠️ Should be in migration/
│   └── vector_store.py (185)       # ✅ Good
│
├── utils/                          # ⚠️ Generic dumping ground
│   └── utils.py (237)              # ❌ Generic name - needs splitting
│
├── scripts/                        # ✅ Good for utilities
│   └── memory_test.py
│
├── docs/                           # ✅ Excellent documentation
│   ├── DOCUMENT_LOADING_OPTIMIZATIONS.md
│   ├── METADATA_FILTERING.md
│   ├── MIGRATION_GUIDE.md
│   ├── SEARCH_OPTIMIZATIONS.md
│   └── ...
│
└── server.py                       # ✅ Good - Entry point
```

## Problems Identified

### 🔴 Critical Issues

#### 1. `rag/core/documents/loader.py` (1121 lines) - TOO LARGE

**Contains 3 separate responsibilities**:
- Core document processing logic (lines 1-400)
- Database-specific loaders (load_event_documents, load_recommendation_documents)
- Module-specific loaders (load_prometheus, load_kubectl, load_aws, etc.) (40+ functions!)

**Impact**:
- Hard to maintain
- Difficult to find specific loaders
- Mixed abstraction levels

#### 2. `controllers/rag_controller.py` (625 lines) - TOO LARGE

**Contains 4 separate responsibilities**:
- Document loading endpoints
- Search endpoints
- Prometheus-specific endpoints
- Knowledge base endpoints (duplicate of knowledgebase_controller.py!)

**Impact**:
- Single file changes affect multiple features
- Difficult to navigate
- Duplicate KB endpoints

#### 3. `utils/utils.py` (237 lines) - Generic Dumping Ground

**Contains unrelated utilities**:
- Config classes
- Module enums
- Database config
- S3 client
- Collection name generation
- Various helpers

**Impact**:
- Everything imports from "utils.utils" (anti-pattern)
- No clear separation of concerns

### 🟡 Medium Issues

#### 4. `migration_lock.py` at wrong level
- Should be in `rag/migration/` not `rag/`

#### 5. Knowledge Base endpoints duplicated
- Both `rag_controller.py` and `knowledgebase_controller.py` have KB endpoints
- Should be consolidated

## Recommended Structure

```
llm/rag-server/
├── config/
│   ├── __init__.py                  # App config
│   ├── database.py                  # NEW - DB configuration
│   └── storage.py                   # NEW - S3/storage config
│
├── controllers/                     # ✅ Keep as-is
│   ├── cache_controller.py
│   ├── collection_controller.py
│   ├── documents_controller.py      # NEW - Split from rag_controller
│   ├── health.py
│   ├── knowledgebase_controller.py  # KEEP - Consolidate KB here
│   ├── migration_controller.py
│   └── search_controller.py         # NEW - Split from rag_controller
│
├── rag/
│   ├── core/
│   │   ├── documents/
│   │   │   ├── __init__.py
│   │   │   ├── collection.py        # ✅ Collection management
│   │   │   ├── processing.py        # NEW - Core processing logic
│   │   │   ├── scraper.py           # ✅ Web scraping
│   │   │   └── loaders/             # NEW - Module loaders package
│   │   │       ├── __init__.py
│   │   │       ├── base.py          # NEW - Base loader utilities
│   │   │       ├── database.py      # NEW - DB loaders (events, recommendations)
│   │   │       ├── cloud.py         # NEW - AWS, GCloud loaders
│   │   │       ├── observability.py # NEW - Prometheus, Loki, ES loaders
│   │   │       ├── k8s.py          # NEW - Kubectl, K8s loaders
│   │   │       └── documents.py     # NEW - PDF, planner, etc.
│   │   │
│   │   ├── embeddings/              # ✅ Keep
│   │   ├── llm/                     # ✅ Keep
│   │   └── monitoring/              # ✅ Keep
│   │
│   ├── migration/
│   │   ├── __init__.py
│   │   ├── lock.py                  # MOVED - From rag/migration_lock.py
│   │   ├── qdrant_exporter.py      # ✅ Keep
│   │   ├── qdrant_importer.py      # ✅ Keep
│   │   └── qdrant_to_qdrant_migration.py # ✅ Keep
│   │
│   ├── qdrant/                      # ✅ Keep
│   ├── search/                      # ✅ Keep - Excellent structure!
│   │
│   ├── exceptions.py                # ✅ Keep
│   └── vector_store.py              # ✅ Keep
│
├── shared/                          # NEW - Shared utilities
│   ├── __init__.py
│   ├── constants.py                 # NEW - Module enums, constants
│   ├── helpers.py                   # NEW - Generic helpers
│   └── naming.py                    # NEW - Collection naming utilities
│
├── scripts/                         # ✅ Keep
├── docs/                            # ✅ Keep - Excellent!
└── server.py                        # ✅ Keep
```

## Migration Plan

### Phase 1: Split Large Files (High Priority)

#### 1.1 Split `rag/core/documents/loader.py`

**Create new files**:
```
rag/core/documents/
├── processing.py                    # Core processing logic
│   ├── process_documents()
│   ├── _generate_document_ids()
│   ├── _create_embeddings_batch()
│   ├── generate_embeddings_batch()
│   ├── process_batch()
│   ├── _setup_collection()
│   └── handle_updated_documents()
│
└── loaders/
    ├── __init__.py                  # Export all loaders
    ├── base.py                      # Base utilities
    │   ├── trim_text()
    │   ├── page_content_default_mapper()
    │   ├── get_active_accounts()
    │   └── chunk_data()
    │
    ├── database.py                  # Database loaders
    │   ├── load_event_documents()
    │   ├── load_recommendation_documents()
    │   ├── load_docs_from_db()
    │   └── load_documents()
    │
    ├── observability.py             # Observability tools
    │   ├── load_prom_json_docs()
    │   ├── load_prom_docs()
    │   ├── load_nb_prom_metrics_metadata()
    │   ├── load_loki_docs()
    │   └── load_es_docs()
    │
    ├── cloud.py                     # Cloud CLI tools
    │   ├── load_aws_docs()
    │   ├── load_gcloud_docs()
    │   └── load_kubectl_docs()
    │
    ├── database_tools.py            # Database tools
    │   ├── load_postgresql_docs()
    │   ├── load_mysql_docs()
    │   └── load_rabbitmq_docs()
    │
    ├── code_tools.py                # Code-related tools
    │   ├── load_github_docs()
    │   └── load_planner_docs()
    │
    ├── documents.py                 # Document loaders
    │   ├── load_kubernetes_docs()
    │   ├── load_pdf_docs()
    │   ├── load_events_json_docs()
    │   ├── load_recommendations_json_docs()
    │   └── load_traces_docs()
    │
    └── account.py                   # Account-specific
        ├── load_account_module_docs()
        └── load_tenant_knowledge_base_docs()
```

**Benefits**:
- Each file < 200 lines
- Logical grouping by domain
- Easy to find specific loader
- Clear dependencies

#### 1.2 Split `controllers/rag_controller.py`

**Create new files**:
```
controllers/
├── documents_controller.py          # NEW - Document loading
│   ├── POST /load_docs
│   ├── POST /load_account_module_docs
│   └── DELETE /delete_collections
│
├── search_controller.py             # NEW - Search endpoints
│   ├── POST /get_matching_doc
│   └── POST /get_prometheus_matching_doc
│
└── knowledgebase_controller.py      # CONSOLIDATE
    ├── POST /kb/create              # Existing
    ├── POST /kb/search              # Existing
    ├── DELETE /kb/{account}/{kb}    # Existing
    ├── POST /knowledge              # MOVED from rag_controller
    ├── GET /knowledge               # MOVED from rag_controller
    └── DELETE /knowledge/{tenant}   # MOVED from rag_controller
```

**Benefits**:
- Single responsibility per controller
- No KB endpoint duplication
- Easier to test and maintain

#### 1.3 Split `utils/utils.py`

**Create new files**:
```
config/
├── database.py                      # NEW
│   └── class DBConfig
│
└── storage.py                       # NEW
    └── s3_client()

shared/
├── constants.py                     # NEW
│   ├── class Module(Enum)
│   └── class Config
│
├── helpers.py                       # NEW
│   └── set_global_trace()
│   └── release_lock()
│
└── naming.py                        # NEW
    ├── get_collection_name()
    └── get_provider_name()
```

**Benefits**:
- Clear module boundaries
- No more "utils.utils" imports
- Easier to find utilities

### Phase 2: Fix Minor Issues (Medium Priority)

#### 2.1 Move `migration_lock.py`

```bash
mv rag/migration_lock.py rag/migration/lock.py
# Update imports in migration_controller.py
```

#### 2.2 Consolidate KB Endpoints

- Remove KB endpoints from `rag_controller.py`
- Keep only in `knowledgebase_controller.py`
- Update API documentation

### Phase 3: Create Missing Structures (Low Priority)

#### 3.1 Add API Models Package

```
api/                                 # NEW - API models
├── __init__.py
├── requests.py                      # All request models
└── responses.py                     # All response models
```

Currently models are scattered in controllers - consolidate them.

#### 3.2 Add Tests Package

```
tests/                               # NEW - Test suite
├── unit/
│   ├── test_search.py
│   ├── test_cache.py
│   ├── test_filters.py
│   └── test_loaders.py
├── integration/
│   ├── test_api.py
│   └── test_qdrant.py
└── conftest.py
```

## Implementation Steps

### Step 1: Create New Structure (No Breaking Changes)

```bash
# Create new directories
mkdir -p rag/core/documents/loaders
mkdir -p shared
mkdir -p config/database.py config/storage.py

# Create empty files
touch rag/core/documents/processing.py
touch rag/core/documents/loaders/{__init__,base,database,observability,cloud,database_tools,code_tools,documents,account}.py
touch shared/{__init__,constants,helpers,naming}.py
touch controllers/{documents_controller,search_controller}.py
```

### Step 2: Move Code (One Module at a Time)

**Example: Move prometheus loaders**

```python
# 1. Create rag/core/documents/loaders/observability.py
# 2. Copy load_prom_json_docs() and related functions
# 3. Update imports
# 4. Test that old import still works via __init__.py
# 5. Update all references to use new path
# 6. Remove old code
```

### Step 3: Update Imports

Use automated refactoring:

```bash
# Find all imports
grep -r "from rag.core.documents.loader import" .

# Update to new paths
# from rag.core.documents.loader import load_prom_json_docs
# → from rag.core.documents.loaders.observability import load_prom_json_docs
```

### Step 4: Deprecation Period

Keep old imports working via `__init__.py`:

```python
# rag/core/documents/loader.py (deprecated)
from rag.core.documents.processing import process_documents
from rag.core.documents.loaders.observability import load_prom_json_docs
# ... re-export everything for backward compatibility
import warnings
warnings.warn("rag.core.documents.loader is deprecated, use loaders/ package", DeprecationWarning)
```

### Step 5: Clean Up

After deprecation period (1-2 releases):
- Remove old files
- Remove backward compatibility imports
- Update all documentation

## Benefits of Proposed Structure

### Developer Experience
✅ **Easy to find code**: Logical grouping by domain
✅ **Small files**: All < 400 lines, most < 200 lines
✅ **Clear dependencies**: No circular imports
✅ **Better IDE support**: Faster autocomplete, easier navigation

### Maintainability
✅ **Single responsibility**: Each file has one job
✅ **Easier testing**: Small, focused modules
✅ **Parallel development**: Multiple devs can work without conflicts
✅ **Clear boundaries**: Know where to add new code

### Performance
✅ **Faster imports**: Only import what you need
✅ **Better caching**: Module-level caching works better with small modules
✅ **Easier optimization**: Profile and optimize specific modules

## Files Modified Summary

### Files to Create (~15 new files)
- `rag/core/documents/processing.py`
- `rag/core/documents/loaders/*.py` (8 files)
- `shared/*.py` (4 files)
- `controllers/documents_controller.py`
- `controllers/search_controller.py`
- `config/database.py`, `config/storage.py`

### Files to Modify
- `rag/core/documents/loader.py` → Becomes backward-compat wrapper
- `controllers/rag_controller.py` → Remove endpoints (moved to new files)
- `controllers/knowledgebase_controller.py` → Add KB endpoints from rag_controller
- `utils/utils.py` → Remove code (moved to shared/)
- `server.py` → Update router imports

### Files to Delete (after deprecation)
- `rag/core/documents/loader.py`
- `utils/utils.py`
- `rag/migration_lock.py`

## Estimated Effort

| Phase | Effort | Risk | Priority |
|-------|--------|------|----------|
| Phase 1.1: Split loader.py | 4-6 hours | Low | High |
| Phase 1.2: Split rag_controller.py | 2-3 hours | Low | High |
| Phase 1.3: Split utils.py | 1-2 hours | Low | Medium |
| Phase 2: Fix minor issues | 1 hour | Very Low | Medium |
| Phase 3: Add missing structures | 4-8 hours | Low | Low |
| **Total** | **12-20 hours** | **Low** | - |

## Next Steps

1. **Review & Approve**: Team review of proposed structure
2. **Create Branch**: `refactor/folder-structure`
3. **Implement Phase 1.1**: Start with splitting loader.py (biggest win)
4. **Test Thoroughly**: Ensure backward compatibility
5. **Repeat for other phases**
6. **Document**: Update README and docs with new structure

## Conclusion

The current structure is **good at the top level** but has **3 critical files that are too large**:
- ✅ `rag/search/` is excellent (recent addition, well-structured!)
- ❌ `loader.py` (1121 lines) needs splitting into loaders package
- ❌ `rag_controller.py` (625 lines) needs splitting into 3 controllers
- ❌ `utils.py` (237 lines) needs splitting into logical modules

**Recommended approach**: **Phase 1.1 first** (split loader.py) - biggest impact with lowest risk.
