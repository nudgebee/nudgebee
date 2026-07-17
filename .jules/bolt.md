## 2026-01-22 - N+1 Query in Dependency Graph Enrichment
**Learning:** `GetServiceDependencyGraph` exhibited a classic N+1 query pattern where it fetched dependencies via Relay and then enriched each one with a separate DB call to `k8s_workloads`. This architecture of "fetch list -> enrich items" is prone to this.
**Action:** Always check loop bodies that enrich data. Use batch fetching (e.g., `WHERE (col1 || '/' || col2) IN (?)`) to resolve N+1 issues when composite keys are involved and ORMs/helpers don't natively support tuple IN clauses easily.

## 2026-05-12 - Batch Size Constraints in DB Batching Optimizations
**Learning:** When replacing row-by-row DB operations with batch statements, always constrain the batch/page size. Large multi-row INSERTs increase PostgreSQL query-parse memory and tuple overhead. `execute_values(page_size=N)` and similar batch APIs need a conservative page_size relative to column count (rows × cols should stay under ~2500-5000 values per statement).
**Action:** In every batching optimization PR, explicitly set and document the page/batch size. Use `rows × columns < 2500` as a rule of thumb. For this codebase's 10-column prediction table, page_size=250 is appropriate.
