# Knowledge Graph — Service Guide

Multi-cloud topology of services, workloads, and cloud resources. Built periodically from cloud APIs + flow data, persisted to PostgreSQL, served to the frontend (ReactFlow), event-investigation, and LLM agents.

**Storage:** PostgreSQL only — no graph DB. Two tables: `knowledge_graph_node`, `knowledge_graph_edge`. BFS/traversal is implemented in Go.

---

## Code Layout

```
knowledge_graph/
├── core/                 # Service struct, BuildGraphs orchestrator, all query methods
│   ├── service.go        (~3.9k lines) entry point — read this first
│   ├── types.go          (~1.1k lines) NodeType/RelationshipType enums, structs, query_attributes
│   ├── helpers.go        node/edge dedup with priority
│   ├── unique_key_builder.go        deterministic UUID from 6-part key
│   ├── cross_account_relationships.go  rule-driven inter-account edges
│   ├── default_relationships.{go,json} 100+ matching rules (JSON embedded)
│   ├── node_matcher.go              cross-source matching
│   ├── filter_repository.go         sync-version + per-tenant filters
│   └── action_service_map.go        event-investigation slice
├── sources/              # Phase 1: per-account static sources (registered via factory)
│   ├── interface.go, registry.go
│   ├── aws_source.go, k8s_source.go, gcp_source.go, azure_source.go
│   └── aws_lb_k8s_enricher.go, k8s_ec2_enricher.go   # Phase 2.1
├── flow_sources/         # Phase 2.5: behavioural edges (CALLS, PUBLISHES_TO)
│   ├── ebpf_flow_source.go, traces_flow_source.go, datadog_apm_flow_source.go
│   ├── service_map_converter.go     trace span pairs → CALLS edges
│   ├── cloud_enrichment.go          ExternalService → real cloud resource
│   ├── enrichment_{aws,azure,gcp}_classifier.go, enrichment_k8s_dns_parser.go
│   ├── ip_mapper.go, dns_resolver.go, eni_resolver.go, matcher.go
│   └── edge_priority.go             k8s > aws > ebpf > traces > datadog-apm
├── queue/                # RabbitMQ async build pipeline
│   ├── publisher.go, consumer.go    consumer holds 1-hour per-tenant lock
│   └── types.go                     KGUpdateMessage
├── models/filters.go
└── integration_test.go   # End-to-end build/save/query
```

**Outside this tree (related):**
- [api-server/services/api/actions_knowledge_graph.go](../api/actions_knowledge_graph.go) — HTTP entry points (`kg_*` actions)
- [api-server/services/api/cron.go](../api/cron.go) — daily 23:30 UTC cron, fans out via queue
- [api-server/services/traces/knowledge_graph_service.go](../traces/knowledge_graph_service.go) — trace→KG bridge
- [collector-server/cloud-collector/account/kg_update.go](../../../../collector-server/cloud-collector/account/kg_update.go) — publishes update on cloud-resource change
- [llm/llm-server/agents/agent_service_dependency_V2.go](../../../../llm/llm-server/agents/agent_service_dependency_V2.go) — KG-only SDG V2 agent
- [llm/llm-server/tools/tool_kg_search.go](../../../../llm/llm-server/tools/tool_kg_search.go), [tool_kg_traverse.go](../../../../llm/llm-server/tools/tool_kg_traverse.go), [tool_kg_get_node.go](../../../../llm/llm-server/tools/tool_kg_get_node.go)

---

## Domain Model

**Unique key (deterministic UUID):**
```
{source}:{cloud_account_id}:{location}:{NodeType}:{hierarchy}:{name}
```
Built in [core/unique_key_builder.go](core/unique_key_builder.go). Same key from any source → same UUID → idempotent upsert.

**Node fields:**
- `properties` (JSONB) — free-form per source
- `query_attributes` (JSONB) — **per-NodeType extracted subset** for fast SQL filtering. Definitions in [core/types.go:763-971](core/types.go#L763)
- `labels`, `language`, `is_active` (tombstone), `source` (which collector emitted)

**Edge dedup constraint:** `(source_node_id, destination_node_id, relationship_type, cloud_account_id, tenant_id)`. Conflicts resolved by source priority — see [flow_sources/edge_priority.go](flow_sources/edge_priority.go).

**Node taxonomy:** 60+ NodeTypes split into Infrastructure vs NonInfrastructure (`InfraAuthoritativeNodeTypes` set in [core/types.go](core/types.go) governs what `markInactiveNodes` is allowed to tombstone — flow sources cannot mark infra nodes inactive).

**Relationship taxonomy:** 25+ RelationshipTypes across service-flow (`CALLS`, `PUBLISHES_TO`), infra (`RUNS_ON`, `BELONGS_TO`), networking (`EXPOSES`, `ROUTES_TO_*`), config/storage (`USES_CONFIG`, `MOUNTS`), identity (`RUNS_AS`, `ASSUMES`).

---

## BuildGraphs — Write Path

[core/service.go:570 BuildGraphs](core/service.go#L570) is the orchestrator. Four phases:

| Phase | What | Key code |
|---|---|---|
| **1** | Per-account static sources produce nodes/edges | `source.BuildGraph(ctx, accountID)` for each registered source |
| **2.1** | Cross-source enrichers join nodes from different sources | `crossSourceEnrichers[i].EnrichCrossSources(graph)` (AWS LB↔K8s Service, Pod↔EC2) |
| **2.5** | Flow sources emit behavioural edges; `CentralizedExternalServiceEnricher` resolves leftover `ExternalService` nodes | [flow_sources/cloud_enrichment.go](flow_sources/cloud_enrichment.go) |
| **3** | Dedup nodes (with ID remap), dedup edges (priority), apply [default_relationships.json](core/default_relationships.json) cross-account rules | [core/helpers.go](core/helpers.go) + [core/cross_account_relationships.go](core/cross_account_relationships.go) |
| **4** | Persist + tombstone | [SaveNodes:1330](core/service.go#L1330) (5K batch), [SaveEdges:1174](core/service.go#L1174) (6K batch), [markInactiveNodes:1493](core/service.go#L1493) |

**Batch sizes** are sized to PostgreSQL's parameter limit (65535). Don't change without recomputing.

**Triggers (build entry points):**
1. **Cron** — daily 23:30 UTC (`build_knowledge_graphs_with_db_filters`) → [cron.go](../api/cron.go) reads enabled `knowledge_graph_tenant_filters` rows → publishes one RabbitMQ message per tenant.
2. **Queue consumer** — [queue/consumer.go](queue/consumer.go) holds a **1-hour per-tenant lock**, runs full BuildGraphs → save → markInactive cycle. Multiple listeners are safe.
3. **Manual / cloud-event** — RPC action `build_knowledge_graph` (synchronous) or cloud-collector publishing on resource change.

**Sync versioning** is incremented only when static sources run (not flow sources). `markInactiveNodes` only tombstones nodes whose `NodeType ∈ InfraAuthoritativeNodeTypes` — flow-source nodes are not tombstoned.

---

## Read Path — Query Methods on `Service`

All on `*Service` in [core/service.go](core/service.go):

| Method | Line | Purpose |
|---|---|---|
| `GetCompleteGraphFromDatabase` | [1776](core/service.go#L1776) | Full tenant graph |
| `GetCompleteGraphFromDatabaseWithFilters` | [1820](core/service.go#L1820) | Filtered: account_ids, node_types, labels, query_attributes (JSONB `@>`) |
| `GetNodeNeighbors` | [2232](core/service.go#L2232) | Single-node neighbours |
| `GetMultipleNodeNeighbors` | [2491](core/service.go#L2491) | Multi-seed BFS, 1–3 levels |
| `SearchNodes` | [3334](core/service.go#L3334) | Name/namespace/cluster/type search |
| `TraverseDirectional` | [3463](core/service.go#L3463) | Directional BFS (upstream/downstream/both) with relationship-type and node-type filters; returns `truncated` flag |

Exposed via RPC actions in [actions_knowledge_graph.go](../api/actions_knowledge_graph.go): `kg_get_complete_graph`, `kg_get_node`, `kg_get_edge`, `kg_get_filter_options`, `kg_get_node_neighbors`, `kg_traverse`, `kg_search_nodes`, `build_knowledge_graph`.

---

## Cross-Service Consumers

**Frontend** — [app/src/components1/KnowledgeGraph.jsx](../../../app/src/components1/KnowledgeGraph.jsx) (ReactFlow + ELK/d3-force, 1500-node cap), via [app/src/api1/kubernetes1/index.ts](../../../app/src/api1/kubernetes1/index.ts) `knowledgeGraph()` calling `kg_get_complete_graph`.

**Event investigation** — [core/action_service_map.go](core/action_service_map.go) auto-fetches a KG neighbourhood when an event has namespace+owner.

**LLM-server (SDG V2 agent)** — flag `llm_server_service_dependency_graph_v2_enabled` flips the `service_dependency_graph` agent from V1 (runtime-metric tool) to V2 (KG-only). V2 exposes:
- `kg_search_nodes` — discovery
- `kg_traverse` — directional BFS, returns `truncated`
- `kg_get_node` — drill-down (gated separately by `KGGetNodeEnabled` flag for payload control)
- `resource_search` — fuzzy name resolver

V2 uses the **ReWOO** planner. V1 and V2 are **mutually exclusive at init time** — when the flag is on, V1's metric-based runtime SDG tool is not registered.

---

## Things to Know Before Editing

- **Don't add `CREATE INDEX CONCURRENTLY` to KG migrations.** golang-migrate wraps each migration in a transaction by default — `CREATE INDEX CONCURRENTLY` cannot run inside one. Use plain `CREATE INDEX`, or add `-- migrate:no-transaction` at the top of the file. (See root [CLAUDE.md](../../../CLAUDE.md#database-migrations--rpc-actions).)
- **Migration timestamps must use current epoch ms** — `python3 -c "import time; print(int(time.time() * 1000))"`. Never hardcode.
- **`query_attributes` is per-NodeType extraction** — when adding a new NodeType, define which `properties` fields get hoisted into `query_attributes` in [core/types.go](core/types.go), or filtered queries against the new type will be slow/empty.
- **Edge priority matters for conflicts.** If two sources emit the same edge with different properties, the priority order in [flow_sources/edge_priority.go](flow_sources/edge_priority.go) decides who wins. Adding a new flow source means deciding where it slots in.
- **Flow sources cannot tombstone infra nodes.** `markInactiveNodes` respects `InfraAuthoritativeNodeTypes` — flow-source-only sync runs do not increment sync_version and do not delete infra.
- **The 1-hour per-tenant lock in the consumer is real** — a stuck or slow build blocks subsequent ones for that tenant for an hour.
- **Frontend caps at 1500 nodes** — bigger graphs need server-side filtering (account_ids, node_types) before the client gets them.

---

## Where to Read First, by Task

| Task | Files in order |
|---|---|
| Add a new node type | [core/types.go](core/types.go) (enum + query_attributes) → relevant `sources/*_source.go` → migration if backfill needed |
| Add a new source | [sources/interface.go](sources/interface.go) → [sources/registry.go](sources/registry.go) → copy structure of [sources/k8s_source.go](sources/k8s_source.go) |
| Add a new flow source | [flow_sources/interface.go](flow_sources/interface.go) → [flow_sources/base_flow_source.go](flow_sources/base_flow_source.go) → [flow_sources/edge_priority.go](flow_sources/edge_priority.go) (add to priority list) |
| Add a cross-account rule | [core/default_relationships.json](core/default_relationships.json) → [core/cross_account_relationships.go](core/cross_account_relationships.go) for matcher behaviour |
| Debug a missing edge | confirm both endpoint nodes exist with correct unique_key → check source priority in [flow_sources/edge_priority.go](flow_sources/edge_priority.go) → check tombstone state (`is_active`) |
| Debug a stuck/slow build | [queue/consumer.go](queue/consumer.go) (lock state) → BuildGraphs phase logs in [core/service.go:570](core/service.go#L570) → batch sizes in SaveNodes/SaveEdges |
| Work on the LLM SDG V2 agent | [llm/llm-server/agents/agent_service_dependency_V2.go](../../../../llm/llm-server/agents/agent_service_dependency_V2.go) → [tool_kg_traverse.go](../../../../llm/llm-server/tools/tool_kg_traverse.go) → [TraverseDirectional:3463](core/service.go#L3463) |

---

## Tests

- Unit: `*_test.go` alongside source. Run with `make test` from `api-server/services`.
- Integration: [integration_test.go](integration_test.go), `core/*_integration_test.go`. Some are gated by `TEST_ACCOUNT` env var (real cloud creds). See [TESTING.md](TESTING.md).
