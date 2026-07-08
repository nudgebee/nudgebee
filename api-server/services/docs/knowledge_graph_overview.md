# Nudgebee Knowledge Graph

**Design Overview, Current State & Developer Guide** — featuring the Services Dependency Agent (shipped).

> This is the narrative/overview doc for the KG: what it is, what's in it today, what's coming, how to
> use it, and the first production agent built on it. For the code-level service guide (file layout,
> build internals, editing gotchas) see [`CLAUDE.md`](CLAUDE.md); for the test setup see
> [`TESTING.md`](TESTING.md).

**Status:** KG in production · Services Dependency Agent V2 (KG-only) merged & in active use.

## Contents

1. [Introduction to the Nudgebee Knowledge Graph](#1-introduction-to-the-nudgebee-knowledge-graph) — what it is, the problem it solves, why it matters
2. [Current State of the KG](#2-current-state-of-the-kg) — nodes and edges in the graph today, and where each comes from
3. [Proposed Additions](#3-proposed-additions) — nodes and edges to add as we progress
4. [How to Use It — Developer Guide](#4-how-to-use-it--developer-guide) — querying the KG, feeding it, the APIs and agent tools
5. [Services Dependency Agent (Shipped)](#5-services-dependency-agent-shipped) — a working KG consumer, tested, merged, in active use
6. [Sample Questions](#6-sample-questions) — concrete examples of using the agent and the KG

---

## 1. Introduction to the Nudgebee Knowledge Graph

### What it is

The Nudgebee Knowledge Graph (KG) is a single, continuously-refreshed model of a customer's entire
technology estate — every service, workload, and cloud resource, and the relationships between them —
across Kubernetes and multiple cloud providers (AWS, GCP, Azure). It answers one deceptively hard
question: **"What is connected to what, and how?"**

Concretely, it is a graph of **60+ node types** (Workload, Database, LoadBalancer, VPC, Namespace,
ServerlessFunction, ContainerRegistry, …) joined by **25+ relationship types** (`CALLS`, `RUNS_ON`,
`EXPOSES`, `MOUNTS`, `ROUTES_TO`, `IS_CONFIGURED_BY`, …). It is built periodically from cloud APIs,
Kubernetes state, and live flow/trace data, then persisted and served to the product, to
event-investigation, and to LLM agents.

### The problem it solves

Modern estates are sprawling and fragmented. The information needed to understand them is scattered
across the cloud console, `kubectl`, Helm values, Terraform state, APM tools, and tribal knowledge in
people's heads. When something breaks — or before you change anything — the questions are always the same:

- What does this service depend on, and what depends on it?
- If this load balancer / database / node fails, what is the blast radius?
- How is this workload connected to the internet, and through which gateways?
- Which workloads call this RDS database / SQS queue / external API?
- What configures, deploys, and hosts this service — down to the Helm chart, image, and git repo?

Answering these by hand means stitching together half a dozen consoles, and the answer is stale the
moment you finish. The KG makes the topology a **queryable, always-current asset** instead of a manual
investigation each time.

### Why it matters

**It is the shared substrate for the rest of the platform.** The same graph powers very different consumers:

- **Visualisation** — the interactive topology map in the product (ReactFlow).
- **Event investigation** — when an event fires, the KG neighbourhood around the affected resource is
  pulled in automatically as context.
- **AI agents** — agents reason over the KG to answer dependency and blast-radius questions in natural
  language (see the Services Dependency Agent in §5).

Because every source resolves to the same deterministic node identity, data from Kubernetes, AWS, GCP
and APM tools converges into **one connected graph** rather than four disconnected islands — that
cross-source join is the core value.

> **Key design choice — no graph database.** The KG lives in two PostgreSQL tables
> (`knowledge_graph_node`, `knowledge_graph_edge`); BFS/traversal is implemented in Go. This keeps the
> operational surface small and reuses the database we already run. Each node's identity is a
> deterministic UUID derived from `{source}:{account}:{location}:{type}:{hierarchy}:{name}`, so the same
> real-world entity seen by two sources collapses to one node (idempotent upsert).

---

## 2. Current State of the KG

This section lists what is in the graph today — the node and relationship taxonomy that is live in
code — and the source each is collected from. Sources fall into two families:

- **Static sources (Phase 1)** — per-account collectors that pull structural inventory from
  provider/cluster APIs: **AWS, Kubernetes (K8s), GCP, Azure**.
- **Flow sources (Phase 2.5)** — behavioural edges from live traffic and traces: **eBPF, distributed
  traces (OTel/Jaeger), GCP Cloud Trace, Datadog APM, New Relic APM, and manual declarations**. These
  add `CALLS` / `PUBLISHES_TO` edges and resolve external endpoints to real cloud resources.

### 2.1 Node types in the graph today

60+ node types, grouped by domain. "Category" distinguishes Infrastructure (tombstoned by the
authoritative source on each sync) from Non-Infrastructure (e.g. workloads/services). Primary source
indicates the collector that authoritatively populates the node.

| Domain | Node types | Primary source(s) |
|---|---|---|
| Compute & workloads | Workload, Service, Pod, Node, Job, CronJob, ComputeInstance, ComputeInstancePool, ServerlessFunction, CustomResource | K8s · AWS · GCP · Azure |
| Orchestration | Cluster, ManagedCluster (EKS/AKS/GKE), Namespace | K8s · AWS · GCP · Azure |
| Networking (K8s) | K8sService, Ingress, NetworkPolicy | K8s |
| Networking (cloud) | LoadBalancer, BackendPool, VPC, Subnet, SecurityGroup, NetworkInterface, RouteTable, NetworkGateway, PrivateEndpoint, PublicIP, APIGateway | AWS · GCP · Azure |
| Data & messaging | Database, Cache, MessageQueue, Queue, Topic | AWS · GCP · Azure (+ flow enrichment) |
| Storage | Storage, PersistentVolume, PersistentVolumeClaim | AWS · GCP · Azure · K8s |
| Config & supply chain | HelmChart, HelmRelease, Configuration, ConfigMap, K8sSecret, Repository, ContainerRegistry, ContainerImage, Artifact, InfraStack | K8s · AWS · GCP |
| DNS & edge | DNSZone, DNSRecord, CDN | AWS · GCP · Azure |
| Identity & security | ServiceIdentity (IAM/MSI/GSA), K8sServiceAccount, SecretVault, EncryptionKey, SecurityService | AWS · GCP · Azure · K8s |
| Observability & misc | MonitoringService, LogAggregator, EmailService, AIService, CloudResource, ExternalService | AWS · GCP · Azure · Flow |

> Node types are defined in [`core/types.go`](core/types.go); the Infrastructure vs
> Non-Infrastructure split is `nodeCategoryMap` / `InfraAuthoritativeNodeTypes` in the same file.

### 2.2 Relationship (edge) types in the graph today

| Domain | Relationship types | Source(s) |
|---|---|---|
| Service flow | `CALLS`, `PUBLISHES_TO`, `SUBSCRIBES_TO` | Flow sources (eBPF, traces, GCP Trace, Datadog/New Relic APM, manual) |
| Hosting & ownership | `RUNS_ON`, `RUNS_IN`, `BELONGS_TO`, `MANAGES`, `OWNS`, `HOSTED_ON` | K8s · AWS · GCP · Azure |
| Supply chain | `IS_DEPLOYED_FROM`, `IS_CONFIGURED_BY`, `USES_IMAGE`, `REFERENCES_IMAGE`, `PULLS_FROM`, `BUILT_FROM` | K8s · cloud registries |
| Networking | `EXPOSES`, `ROUTES_TO_SERVICE`, `ROUTES_TO_BACKEND`, `ROUTES_TO`, `ROUTES_THROUGH`, `RESOLVES_TO`, `IS_ACCESSED_VIA`, `ASSOCIATED_WITH`, `PROTECTS` | K8s · AWS · GCP · Azure |
| Config, secrets, storage | `USES_CONFIG`, `USES_SECRET`, `USES_SERVICE_ACCOUNT`, `STORES_IN`, `MOUNTS`, `IS_BOUND_TO`, `PROVIDES_STORAGE` | K8s · cloud |
| Identity | `RUNS_AS`, `ASSUMES` | AWS · GCP · Azure (IAM/MSI/GSA) |
| Encryption | `IS_ENCRYPTED_BY` | AWS · GCP · Azure (schema; sparsely populated) |
| Telemetry | `EMITS_LOGS_TO`, `EMITS_METRICS_TO`, `EMITS_TRACES_TO` | Schema present; population in progress |

Edge conflicts (same source→destination→type seen by more than one source) are resolved by a fixed
priority (see `flow_sources/edge_priority.go`; lower number wins):

```
k8s  >  manual  >  aws  >  ebpf  >  traces  >  datadog-apm  >  newrelic-apm
```

(`gcp-cloud-traces` shares the same priority level as `traces`; unknown sources rank lowest.)

### 2.3 How the graph is built (write path)

A build runs in four phases, orchestrated by `BuildGraphs` in the api-server
([`core/service.go`](core/service.go)):

| Phase | What happens |
|---|---|
| 1 · Static sources | Each per-account source (AWS, K8s, GCP, Azure) emits nodes + structural edges from provider/cluster APIs. |
| 2.1 · Cross-source enrichers | Join nodes discovered by different sources — e.g. AWS LoadBalancer ↔ K8s Service, Pod ↔ EC2 instance. |
| 2.5 · Flow sources | Add behavioural `CALLS` / `PUBLISHES_TO` edges from traffic & traces; resolve leftover `ExternalService` nodes to real cloud resources. |
| 3 · Dedup & rules | Deduplicate nodes (ID remap) and edges (by priority); apply 100+ cross-account matching rules. |
| 4 · Persist & tombstone | Batch-upsert nodes/edges to PostgreSQL; mark stale infra nodes inactive (`is_active=false`). |

**Triggers:** an hourly cron (every `:30` UTC) fans out one build job per enabled tenant over RabbitMQ;
a queue consumer runs the build under a **1-hour per-tenant lock**; and cloud-collectors publish an
update when resources change. A synchronous `build_knowledge_graph` RPC action exists for manual /
on-demand builds.

---

## 3. Proposed Additions

The taxonomy already defines several relationship types that are present in the schema but not yet
populated by any source — validation (§5) confirmed these return correct-but-empty results today.
Closing those gaps, plus a few genuinely new node/edge families, is the natural next progression.
Listed roughly in priority order.

### 3.1 Populate edges that already exist in the schema

| Relationship | What it unlocks | How to populate |
|---|---|---|
| `RUNS_AS` / `ASSUMES` | Identity & blast-radius: "what can this workload assume?", IAM trust chains, IRSA. | AWS IAM / GCP SA / Azure MSI; K8sServiceAccount IRSA annotations → ServiceIdentity. |
| `EMITS_LOGS_TO` / `_METRICS_TO` / `_TRACES_TO` | "Where do this service's logs/metrics/traces go?" — closes the observability loop. | Derive from agent/exporter config; the trace flow source already knows trace destinations. |
| `IS_ENCRYPTED_BY` | Security posture: which KMS/Key Vault key protects a DB/volume/secret. | AWS KMS / GCP Cloud KMS / Azure Key Vault associations on storage & DB inventory. |
| `STORES_IN` | K8s Secret → external SecretVault (e.g. External Secrets Operator). | Parse ExternalSecret/SecretStore CRDs and CSI secret mounts. |
| `ROUTES_THROUGH` / BackendPool `MANAGES` | Complete LB → target-group → instance/pod routing for accurate blast radius. | AWS target-group / GCP backend-service / Azure backend-pool membership. |
| InfraStack `MANAGES` | IaC ownership & change attribution — "what Terraform/CFN stack owns this resource?" | CloudFormation stacks, Terraform state, ARM deployments → MANAGES edges. |

### 3.2 New node & edge families to consider

- **Ownership graph** — a Team / Owner node with `OWNS` edges to services, so "who owns X / who do I
  page" is a graph query. Source: labels, CODEOWNERS, catalog import.
- **Cost attribution** — attach cost/spend as node attributes (or a Cost edge), letting blast-radius
  and dependency views double as cost-impact views.
- **Change/Deployment events** — temporal Deployment/Release nodes linked to workloads, enabling "what
  changed right before this incident?" correlation.
- **Incident / alert linkage** — connect active alerts and incidents to the nodes they affect, so
  investigation agents traverse straight from symptom to topology.
- **Data classification** — tag Database/Storage nodes with PII/data-sensitivity, powering compliance
  and "which services touch sensitive data" queries.
- **More flow sources** — extend behavioural coverage (e.g. additional APM/mesh sources) and slot them
  into the edge-priority order.

> **Guiding principle:** add a node/edge type only when a consumer (the product, investigation, or an
> agent) will actually traverse it. Every new NodeType also needs its `query_attributes` extraction
> defined, or filtered queries against it will be slow/empty.

---

## 4. How to Use It — Developer Guide

Two audiences use the KG: developers/services querying it over RPC, and LLM agents querying it through
a small set of tools. Both hit the same PostgreSQL-backed graph and the same Go traversal engine.

### 4.1 The mental model

- **Nodes** have a stable `id` (deterministic UUID), a `node_type`, a `name`, free-form `properties`
  (JSONB), and a per-type `query_attributes` subset hoisted out for fast SQL filtering (namespace,
  cluster, account, labels…).
- **Edges** are directional: `(source_node_id) →[relationship_type]→ (destination_node_id)`, scoped by
  account + tenant.
- **Direction matters.** "downstream" follows edges out (what X depends on / points to); "upstream"
  follows edges in (what depends on / points to X). "both" expands in both directions.

### 4.2 Querying the KG — RPC actions

Backend services and the frontend call these RPC actions (handlers in
[`../api/actions_knowledge_graph.go`](../api/actions_knowledge_graph.go)). Query methods live on the KG
`Service` in [`core/service.go`](core/service.go).

| RPC action | Backing method | Use it for |
|---|---|---|
| `kg_list_nodes` (alias `kg_search_nodes`) | `SearchNodes` | Discovery / search by name, type, namespace, cluster, labels. |
| `kg_list_path` (alias `kg_traverse`) | `TraverseDirectional` | Directional BFS (upstream/downstream/both) with relationship & node-type filters; returns a `truncated` flag. |
| `kg_get_node` | `GetNodeByID` | Drill into one node's full properties. |
| `kg_get_complete_graph` | `GetCompleteGraphFromDatabase` / `…WithFilters`, or `GetMultipleNodeNeighbors` when `node_ids` are supplied | Full (or filtered) tenant graph — used by the topology UI (caps ~1500 nodes); with `node_ids`, the 1–3 level neighbourhood around those seeds. |
| `kg_get_edge` | `GetEdgeByID` | Inspect a single edge. |
| `kg_get_filter_options` | `GetFilterOptions` | Fetch available filter values for the UI. |
| `kg_get_filter_values` | `GetFilterValues` | Fetch available values for a specific filter key. |
| `build_knowledge_graph` | `BuildGraphs` | Trigger a synchronous on-demand rebuild for a tenant. |

### 4.3 The traversal contract (important semantics)

- **Filters:** `exclude_node_types` and `relationship_types` are applied during traversal. `node_types`
  (include) is **NOT** applied inside the recursive walk — it would prune intermediate hops needed to
  reach a deeper target. The full connecting subgraph is returned so an agent can see the path; filter
  the result client-side if you only want leaf types. (This was a bug found & fixed during validation —
  see §5.)
- **Depth:** `max_depth` bounds hops (typically 1–3). `max_nodes` caps result size; when hit, the
  response sets `truncated: true` and reports `total_discovered`.
- **Validation:** passing both `node_ids` and search params is a `400` (conflicting inputs).
- **Performance:** queries return in < 1s; even a 152-node / 1,087-edge VPC traversal is sub-second.

### 4.4 How agents query it — the tool surface

LLM agents do not see SQL or RPC. They are given three composable tools, which map onto the actions above:

| Agent tool | Maps to | Purpose |
|---|---|---|
| `kg_search_nodes` | `kg_list_nodes` | Resolve a name/intent to concrete node(s) — the entry point of almost every query. |
| `kg_traverse` | `kg_list_path` | Walk upstream/downstream/both from resolved node(s) to find dependencies & blast radius. |
| `kg_get_node` | `kg_get_node` | Pull full detail on a specific node when the agent needs attributes. |

A typical agent flow is two calls: **search** to resolve the entity, then **traverse** to explore from
it (the validation report calls these "multi-step" questions). A fuzzy `resource_search` resolver helps
when the user's name doesn't exactly match a node. Tool implementations live in
[`../../../llm/llm-server/tools/`](../../../llm/llm-server/tools/) (`tool_kg_search.go`,
`tool_kg_traverse.go`, `tool_kg_get_node.go`).

### 4.5 How to feed it (write path for new data)

- **Add a new structural source** (e.g. a new cloud or inventory system): implement the source
  interface, register it in the source registry, and model your output on
  [`sources/k8s_source.go`](sources/k8s_source.go). It runs in Phase 1.
- **Add a new flow source** (behavioural edges): implement the flow-source interface on top of
  [`flow_sources/base_flow_source.go`](flow_sources/base_flow_source.go) and add the source to
  [`flow_sources/edge_priority.go`](flow_sources/edge_priority.go) so conflicts resolve deterministically.
- **Add a new node type:** add the enum + its `query_attributes` extraction in
  [`core/types.go`](core/types.go), emit it from the relevant source, and add a migration if a backfill
  is needed.
- **Add a cross-account rule:** declare it in
  [`core/default_relationships.json`](core/default_relationships.json) (100+ rules already drive
  inter-account edges).

Two guardrails to remember: **flow sources cannot tombstone infrastructure nodes** (only authoritative
static sources increment `sync_version` and mark infra inactive); and **KG migrations must use plain
`CREATE INDEX`** (never `CREATE INDEX CONCURRENTLY` — golang-migrate wraps each migration in a
transaction).

---

## 5. Services Dependency Agent (Shipped)

The Services Dependency Agent is the first production proof that the KG is more than a picture — it is
a reasoning substrate. It is **shipped**: tested, merged, and actively used to answer dependency and
blast-radius questions and to ground investigations.

| | |
|---|---|
| **Status** | Merged & in active use — V2 (KG-only) merged 30 Apr 2026 (PR #29505) |
| **What it does** | Answers natural-language questions about service dependencies, connectivity, and blast radius by reasoning over the KG |
| **How it works** | KG-only agent using the ReAct3 planner; tools = `kg_search_nodes`, `kg_traverse`, `kg_get_node`, `resource_search` |
| **Feature flag** | `llm_server_service_dependency_graph_v2_enabled` flips the agent from V1 (runtime-metric tool) to V2 (KG). V1 and V2 are mutually exclusive at init. |
| **Validation** | 47 / 47 scripted API scenarios passed; one real bug found & fixed during testing |
| **Real-world usage** | 264 production invocations (6 May – 17 Jun 2026), 99% success (263/264) |

Agent code: [`../../../llm/llm-server/agents/agent_service_dependency_V2.go`](../../../llm/llm-server/agents/agent_service_dependency_V2.go).

### 5.1 From V1 to V2 — why the KG version is better

V1 inferred dependencies from runtime metrics alone. V2 reads the KG, so it sees the full structural +
behavioural topology — not just "who talked to whom recently" but also what hosts, configures, deploys,
and exposes each service, across cloud and Kubernetes. The flag-gated, mutually-exclusive cutover let
us ship V2 safely and fall back instantly if needed.

### 5.2 Validation report (KG Traversal API)

**Date:** 2026-04-16 · **Tenant:** `890cad87-c452-4aa7-b84a-742cee0454a1` · **Server:** `localhost:8000`

| Category | Total | Pass | Fail | Notes |
|---|---|---|---|---|
| Discovery & Search | 12 | 12 | 0 | All search filters working |
| Dependency Exploration | 9 | 9 | 0 | Downstream traversal verified |
| Reverse Lookup / Upstream | 8 | 8 | 0 | Fixed CTE node_types bug during testing |
| Networking & Connectivity | 8 | 8 | 0 | `exclude_node_types` verified |
| Infrastructure & Hosting | 7 | 7 | 0 | 0-result queries = missing data, not bugs |
| Multi-Step (2 API calls) | 3 | 3 | 0 | search → traverse pipeline verified |
| **TOTAL** | **47** | **47** | **0** | |

#### Bug found & fixed during testing

- **Symptom** — "What workloads run on cluster k8s-prod?" (upstream, `max_depth` 2, `node_types=[Workload]`)
  returned 0 results.
- **Root cause** — the `node_types` include-filter was applied inside the recursive CTE, blocking the
  intermediate Namespace nodes needed to reach Workloads at depth 2.
- **Fix** — removed `node_types` from the CTE; only `exclude_node_types` and `relationship_types` apply
  during traversal, so the full connecting subgraph is returned. After the fix, the query returns 121
  nodes (1 Cluster, 13 Namespaces, 84 K8sServices, 8 Nodes, 7 PVs, 7 PVCs, 1 Workload).

This is the contract documented in §4.3 — a real bug the validation pass caught and corrected.

#### Verified capability highlights

| Capability | Verified example |
|---|---|
| Discovery | Find all databases (20: gcp+aws); all VPCs (17); all load balancers (18); workloads by label `app=kibana`; GKE/EKS clusters. |
| Downstream deps | *What does llm-server depend on?* → 13 nodes / 13 edges across ContainerRegistry, HelmChart, K8sService, Namespace, Cluster, Repository. |
| Upstream / blast radius | *What uses VPC `vpc-00459…`?* → 152 nodes (ComputeInstance, Database, LB, NetworkInterface, SecurityGroup, Storage, Subnet). |
| Networking | Trace LB → workload path; NLB routing; LB → EC2 instances; `exclude_node_types` prunes SecurityGroup/Subnet noise. |
| Multi-step | Helm charts for all workloads in a namespace; compare llm-server vs relay-server deps; registries for all nudgebee workloads. |

A handful of queries returned 0 neighbours — **not bugs**, but relationship types that exist in the
schema yet are not populated by any source today (`RUNS_AS`/`ASSUMES`, `IS_ENCRYPTED_BY`,
`EMITS_LOGS_TO`, `ROUTES_THROUGH`, BackendPool/InfraStack `MANAGES`). These are exactly the gaps §3
proposes to close.

---

## 6. Sample Questions

Concrete examples of how to use the agent and the KG. The first set is drawn from real production
invocations of the Services Dependency Agent; the second shows the underlying two-call (search →
traverse) pattern any consumer can use directly.

### 6.1 Real questions users asked the agent

**Dependency & connectivity**

- What are the dependencies of llm-server in the nudgebee namespace?
- How is llm-server connected to other services?
- From which services does services-server get data in the nudgebee namespace?
- Show me the full dependency chain for ml-k8s-server in the nudgebee namespace (account k8s-prod).
- What does relay-server in the nudgebee namespace call?
- Tell me all the communication happening in the nudgebee namespace.

**Blast radius & failure impact**

- What services and resources are affected if load balancer "Nudgebee-ALB-Test" in dev-aws fails? Map the blast radius.
- What depends on the production load balancer? Show the downstream blast radius if it fails.
- What are the upstream dependencies for statefulset redis-master in namespace redis?

**Cloud resources & cross-cloud**

- Which Kubernetes workloads call or depend on AWS RDS databases?
- Show all SQS message queues in the dev-aws account and which workloads publish to or subscribe from them.
- Which databases does the nudgebee namespace use, and which workloads call them?
- What external services (third-party APIs, public IPs) do workloads in the nudgebee namespace call? Group by workload.
- Describe the AWS prod cloud infrastructure.
- How is the prod cluster connected to the internet? Show topology, load balancers, and egress gateways.

### 6.2 The two-call pattern (search → traverse)

Under the hood almost every answer is: resolve the entity, then walk from it. For example,
*"What does llm-server depend on?"*:

```
# 1. Resolve the entity
kg_search_nodes(name="llm-server", node_type="Workload", namespace="nudgebee")
   -> node_id = "…"

# 2. Walk downstream from it
kg_traverse(node_ids=["…"], direction="downstream", max_depth=3)
   -> 13 nodes, 13 edges: ContainerRegistry, HelmChart, K8sService,
      Namespace, Cluster, Repository
```

Reverse the direction to get blast radius (*"what depends on llm-server?"* → `direction="upstream"`);
add `exclude_node_types=["SecurityGroup","Subnet","NetworkInterface"]` to strip networking noise from a
connectivity view; set `max_nodes` to bound large results (the response flags `truncated` and reports
`total_discovered`).

### 6.3 A blast-radius example end to end

**Question:** *"What uses VPC `vpc-00459f012dc59d416`?"*

```
kg_search_nodes(name="vpc-00459f012dc59d416", node_type="VPC")  -> node_id
kg_traverse(node_ids=[node_id], direction="upstream", max_depth=2)
   -> 152 nodes / 1,087 edges in < 1s:
      ComputeInstance, Database, LoadBalancer, NetworkInterface,
      SecurityGroup, Storage, Subnet
```

That single answer — the full set of resources riding on one VPC — is the kind of question that used to
mean an afternoon in the AWS console. With the KG it is one traversal, and the agent renders it in plain
English.

---

*Nudgebee Knowledge Graph — internal engineering document. See [`CLAUDE.md`](CLAUDE.md) for the
code-level service guide.*
