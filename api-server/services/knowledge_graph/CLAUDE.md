# Knowledge Graph — Service Guide

Multi-cloud topology of services, workloads, and cloud resources. Built periodically from cloud APIs + flow data, persisted to PostgreSQL, served to the frontend (ReactFlow), event-investigation, and LLM agents.

**Storage:** PostgreSQL only — no graph DB. The graph itself is two tables: `knowledge_graph_node`, `knowledge_graph_edge`. BFS/traversal is implemented in Go. A third side table, `knowledge_graph_classification_review`, holds near-miss classifications and is never read by graph queries (see [Uncertain Classifications](#uncertain-classifications--the-near-miss-side-channel)).

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
│   ├── classification_review.go     records near-miss classifications (source-agnostic)
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
│   ├── manual_*.go                  user-declared dependencies and nodes (see Manual Nodes & Dependencies)
│   └── edge_priority.go             k8s > manual > aws > ebpf > traces > datadog-apm
├── queue/                # RabbitMQ async build pipeline
│   ├── publisher.go, consumer.go    consumer applies resource refs, then enqueues + claims builds
│   └── types.go                     KGUpdateMessage, KGResourceRef
├── changedetect/         # 10-min inventory watermark detector → delete/create/rollout refs or account rebuilds
├── resourcepatch/        # applies one resource's update/delete/create to the graph without a rebuild
├── models/filters.go
└── test/                 # E2E test suite (package test) + testdata/
    ├── e2e_*_test.go     Tier A (JSON→graph goldens), Tier B (DB-gated), mocked-infra
    ├── integration_test.go   phase build/save/query + shared fixture loaders
    └── testdata/e2e/     per-scenario input JSON + golden.json
```

**Outside this tree (related):**
- [api-server/services/api/actions_knowledge_graph.go](../api/actions_knowledge_graph.go) — HTTP entry points (`kg_*` actions)
- [api-server/services/api/cron.go](../api/cron.go) — hourly cron (`30 * * * *`), fans out via queue
- [api-server/services/traces/knowledge_graph_service.go](../traces/knowledge_graph_service.go) — trace→KG bridge
- [collector-server/cloud-collector/account/kg_update.go](../../../collector-server/cloud-collector/account/kg_update.go) — publishes update on cloud-resource change
- [llm/llm-server/agents/agent_service_dependency_V2.go](../../../llm/llm-server/agents/agent_service_dependency_V2.go) — KG-only SDG V2 agent
- [llm/llm-server/tools/tool_kg_search.go](../../../llm/llm-server/tools/tool_kg_search.go), [tool_kg_traverse.go](../../../llm/llm-server/tools/tool_kg_traverse.go), [tool_kg_get_node.go](../../../llm/llm-server/tools/tool_kg_get_node.go)

---

## Domain Model

**Unique key (deterministic UUID):**
```
{source}:{cloud_account_id}:{location}:{NodeType}:{hierarchy}:{name}
```
Built in [core/unique_key_builder.go](core/unique_key_builder.go). Same key from any source → same UUID → idempotent upsert.

**Node fields:**
- `properties` (JSONB) — free-form per source
- `query_attributes` (JSONB) — extracted `properties` subset for fast SQL filtering. Driven by the **per-`specific_type` concrete schema** (`core.SpecificTypeSchema`, declared co-located in `sources/<cloud>/*.go`, its `Indexed` fields are hoisted), **unioned** with the per-NodeType `QueryablePropertiesMap` fallback ([core/types.go](core/types.go)) so un-schema'd nodes still work and no filter can regress. Engine: [core/schema_registry.go](core/schema_registry.go).
- `node_type` — the **ontological** (cloud-agnostic) type, e.g. `ComputeInstance`. Part of the unique key.
- `specific_type` — the **concrete cloud/native** type, e.g. `EC2Instance`/`GKECluster`/`KubernetesDeployment`. A parallel, queryable column; **not** in the unique key. A source declares it by setting `properties["specific_type"]` (cloud sources via the label on each resource's kind, `sources/<cloud>/kinds.go`; k8s per-module); `NewNode` lifts it into the column.
- `ontology_attributes` (JSONB) — the **cross-cloud-normalized field schema** for the node's NodeType (every `ComputeInstance` exposes the same keys: `name/region/type/state/...` regardless of cloud). Populated by the ontology registry ([core/ontology_registry.go](core/ontology_registry.go) engine + `core/ontology_data_<cloud>.go` data + `core/ontology_vocab.go` allowlist), keyed by `specific_type`.
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
1. **Cron** — hourly at `:30` UTC (`build_knowledge_graph` in `runbook-server/internal/system/cron_triggers.yaml`) → [cron.go](../api/cron.go) publishes one tenant-wide message per tenant with an enabled filter. This is the reconciliation pass.
2. **Queue consumer** — [queue/consumer.go](queue/consumer.go) records each message in `knowledge_graph_build_requests`, claims the tenant under a per-tenant advisory lock, and builds the union of the pending scopes (tenant-wide or account-scoped, see [core/build_request_repository.go](core/build_request_repository.go)). Tenant-wide builds are throttled by `kg_update_deduplication_minutes`; account-scoped ones are not.
3. **Account-scoped requests** — messages with `account_ids`: the cloud-collector's daily post-report job and its events job (when resources were created/deleted), and the inventory change detector ([changedetect/](changedetect/), cron `kg_detect_inventory_changes` every 10 min at `:x5`) for changes it cannot express per resource. The detector compares each account's active count and newest `created_at` / `creation_time` / `node_creation_time` with marks saved in `knowledge_graph_inventory_marks` (a mark is only ever compared with its own column). A count drop is resolved to the graph nodes whose inventory row is gone (delete refs); new rows of creatable types become upsert refs; new EC2 instances, K8s workloads/nodes, re-activated rows, or more than 50 refs rebuild the account.
4. **Per-resource refs** — messages with `resources` (AWS EventBridge and Azure EventGrid upserts, k8s-collector workload and node deletions, detector deletes/creates/rollouts). The consumer hands them to [resourcepatch/](resourcepatch/) *before* enqueueing. **Update:** the node is rebuilt from its inventory rows with the source's own builder and saved only if its id and type match the stored node; its own outgoing edges are rewritten when its type declares an edge signature. **Delete:** when the inventory no longer has the resource, the node and every incident edge are tombstoned (`"Node"` refs are resolved by Kubernetes node name). **Create:** a node of a creatable type is added with its own-row outgoing edges; edges other resources point at it, and edges needing live cloud calls, arrive with the next account build. Anything it cannot prove (rename, account-level kind, non-creatable new resource, running build) becomes an account-scoped build request. Every patch runs under the tenant's build lock (`BuildRequestRepository.ExcludeBuilds`, the same `kg_build:<tenant>` lock claims take), so a build running when the patch starts makes it stand aside, and no build can be claimed until the patch has written — a patch and a build of the same account never interleave. Patched rows are also stamped `max(current, node) + 1`, so a build claimed right after does not sweep them. **Rollback:** `kg_resource_patch_enabled` (default **true**) turns every ref into its account's rebuild at the consumer boundary when set false, whoever published it; `kg_inventory_change_detection_enabled` stops the detector; `kg_inventory_change_creates_enabled` stops per-resource creates only.

**Where per-resource rules live.** `resourcepatch` and `changedetect` hold no resource knowledge. Each resource file declares a `sources.PatchSpec` in its `init()` via `registerPatchSpec` — next to its builder (`sources/aws/sqs.go`, `lambda.go`, `ec2.go`, …): `AccountLevel`, `Creatable`, `Context` (edge-target node types) and `Signature` plus the edge builder. `sources/<cloud>/patch.go` assembles them into a `sources.CloudPatchPolicy`; Kubernetes rules live in `sources/k8s/workload.go` and `node.go` behind `sources.K8sPatchPolicy`. A type without a spec gets the safe default: properties refresh, nothing else.

**Adding or changing a patch spec:** declare it in the resource's file. Two tests judge it: `sources/<cloud>/patch_spec_test.go` (names are real specific types; no signature contains an edge another pass emits, such as `IS_ENCRYPTED_BY`; account-wide-pass types are account-level) and `test/e2e_resource_patch_drift_test.go` (update mode: same node id and signature edges as the full build; create mode: a creatable type's full-build edges outside its signature must be ones other passes emit). When a source gains an account-wide pass that creates, re-keys or reshapes nodes (`ensure*`, `collapse*`, `propagate*`), declare that node type `AccountLevel` in the file that owns it.

**What a resource file owns.** A cloud resource is described in one file, top to
bottom: its concrete schema (`core.RegisterSpecificTypeSchema`), the inventory
rows it claims (`registerKind` — node type, concrete label, subtype, static
properties, its metadata extractor), the edges the account build asks it for
(`registerEdgeStep`, AWS and Azure; GCP's builders are a fixed pipeline in
`gcp_source.go` because one of them feeds the next), and how one of its
resources is patched alone (`registerPatchSpec`). Kubernetes kinds declare the
same way through `registerWorkloadKind` (node type, whether the build queries
for it, whether the detector watches it, whether one row is patchable, its
spec-field extractor) plus `registerClusterScopedKind` for keys with no
namespace. The engines are `sources/resource_kind_registry.go` (shared),
`sources/<cloud>/kinds.go` and `sources/{aws,azure}/dispatch.go`; `classify.go`
and `convertResourcesToGraph` hold no per-resource knowledge.

**Adding a cloud resource type.** Add one file, or extend the resource's
existing one: schema, `registerKind`, an extractor if its metadata carries
anything, an edge step if it has edges of its own, a patch spec if one of its
resources can be updated alone. Four tests judge it — `dispatch_completeness`
(the kind resolves to what it declares), `all_types_completeness` (a synthetic
row of it actually produces a node), `schema_coverage` and `ontology_coverage`
(its label has both schemas) — and they read the registry, so nothing has to be
added to a list elsewhere. A row no kind claims becomes a `CloudResource`; a
node type no step claims gets the default VPC/VNet attachment. Two rules stay
central on purpose: the service/type include filters (`DefaultServiceTypeFilter`
and friends, which are injected configuration) and the row suppressions that are
predicates over a row rather than identity (`shouldSuppressGCPResource`,
`shouldSuppressAzureResource`) — the patch policy applies those same predicates
through `IncludeForGraph`.

**Edges another pass emits** (KMS encryption, log groups, CloudFormation
`MANAGES`, a Lambda's event-source mapping) are declared by the pass that emits
them, via `registerEdgesBuiltElsewhere` in its own file. A patch signature must
never contain one; `patch_spec_test.go` and the drift test read that declaration
rather than a literal list.

**Sync versioning** is incremented only when static sources run (not flow sources). `markInactiveNodes` only tombstones nodes whose `NodeType ∈ InfraAuthoritativeNodeTypes` — flow-source nodes are not tombstoned.

---

## Uncertain Classifications — the Near-Miss Side Channel

When a classifier tries every rule it has and still isn't confident enough to decide *what* a thing is (or *which* existing node it is), the signal it saw on the way used to vanish. It's now recorded instead, so a too-aggressive confidence guard's false negatives can be reviewed and backtracked on without re-running ingestion.

**This never changes the graph.** No node, no edge, no `properties.type_evidence` — those still reflect only confident classifications. It writes one row to `knowledge_graph_classification_review` and nothing else.

**Recorder:** [`core.RecordUncertainClassification(ctx, dbManager, core.UncertainClassificationCandidate{...})`](core/classification_review.go) — source-agnostic and **fire-and-forget by construction**: it returns nothing, so no caller can let a failure here affect build success. Pass a short-timeout context derived from `context.Background()`, **not** the build's request context — a cancelled build must not turn this into an error path.

| Field | Meaning |
|---|---|
| `Source` | who saw it — `"traces"` today; `"ebpf"` / `"aws"` / `"gcp"` / `"azure"` / `"k8s"` are the intended next ones |
| `ClassificationKind` | which *kind* of decision was uncertain — `"node_type"` today; `"specific_type"` / `"node_match"` are reserved |
| `CandidateType` | the type/value that was rejected or left unmapped |
| `ReasonCode` | which guard fired — free-form, but keep one code per distinct guard |
| `Evidence` | JSONB; whatever lets a human re-find the signal (trace/span ids, matched key) |

**Row identity** is `(tenant_id, source, classification_kind, candidate_name, candidate_namespace, candidate_cluster)`. Repeat sightings overwrite reason/type/evidence with the latest occurrence and bump `occurrence_count` — latest-wins, no history array. `candidate_cluster` is in the key because the same namespace/service name in two clusters is two independent near-misses, and `candidate_namespace`/`candidate_cluster` are `NOT NULL DEFAULT ''` because Postgres `UNIQUE` treats `NULL <> NULL` (nullable would let two "no namespace" sightings insert as duplicates instead of colliding onto one row).

### Wiring a new source into it

Traces is the reference implementation; the shape generalises:

1. **Producer** — the classifier returns the near-miss alongside its answer instead of dropping it. [`traces.detectApplicationType`](../traces/helpers.go) returns a third value, `*UncertainMatch`, populated only when a guard excluded a pattern that *would* have matched. First-write-wins within a single classification pass.
2. **Carrier** — hang it on whatever struct already survives to the flow/source layer (`traces.ServiceApplication.UncertainMatch`), so aggregation across spans/resources keeps the first near-miss rather than the last.
3. **Flush** — call the recorder once per processed item per build, at [`recordUncertainClassificationIfAny`](flow_sources/traces_flow_source.go), so `occurrence_count`/`last_seen_at` track every rebuild the near-miss is still present in.
4. **Suppress the settled cases** — skip recording when the thing already resolves confidently *for the decision in question* (traces skips when `inferNodeType` already yields a Database/Cache/MessageQueue override). Deliberately **not** gated on "any confident signal at all": most services get a language tag from some span, and the motivating case had no language signal — gating that broadly suppresses exactly what this exists to catch.

**Reason codes in use today** (all `source="traces"`, `classification_kind="node_type"`):

| Code | Guard that fired |
|---|---|
| `outbound_span_kind` | span kind is CLIENT/PRODUCER/CONSUMER — the name describes the callee, not this service |
| `operation_verb_span_name` | no span kind at all, but the name carries an operation verb (consume/process/send/…) and no SERVER kind confirms this service is the destination |
| `messaging_system_name_mismatch` | `messaging.system` is set but the service name doesn't contain it |

`source="aws-vpc-flow"`, `classification_kind="node_match"` — a private flow-log address no lookup could place (`candidate_name` = the IP, `candidate_namespace` = its VPC or region, `candidate_cluster` = the AWS account). The graph shows these as one "Unidentified addresses (<vpc>)" node per VPC; the per-IP detail lives here. See `flow_sources/vpc_flowlogs_k8s_resolution.go`.

| Code | Why the address stayed unresolved |
|---|---|
| `vpc_ip_unresolved` | no ENI, K8s node or K8s pod held it during the flow window (or no K8s account is mapped / metrics were unavailable — see `lookups_tried`) |
| `vpc_ip_k8s_node_gone` | it was a K8s node's IP during the window, but that node is no longer in the graph; not attributed to a pod because host-network pods share the node IP |
| `vpc_ip_ambiguous_owner` | more than one workload's pods held it during the window (IP reuse) |
| `vpc_ip_workload_not_in_graph` | exactly one workload held it, but the K8s source didn't emit that workload |

When adding a guard, give it its **own** reason code rather than reusing a near-neighbour — a review row labelled `outbound_span_kind` for a span that carried no span kind is worse than no row, because it sends whoever reads it looking for something that was never there.

---

## Manual Nodes & Dependencies

Users declare what the collectors cannot see. Both halves are run by the always-on `manual` flow source ([flow_sources/manual_flow_source.go](flow_sources/manual_flow_source.go)) every build.

- **Dependencies** (`kg_manual_dependencies`, V743) — `A CALLS B` between nodes, resolved by type + name (+ namespace/cluster/arn) against `knowledge_graph_node` and emitted as `source='manual'` edges.
- **Nodes** (`kg_manual_nodes`, V989) — resources no collector discovered, so a dependency has something to resolve to. RPCs: `kg_*_manual_node*` in [api/actions_kg_manual_nodes.go](../api/actions_kg_manual_nodes.go).

**A declared node must get the id the collector will write for the same resource**, or the collector later adds a second node. So nothing hand-builds a cloud key: each cloud registers a `core.ManualNodeBuilder` ([sources/manual_node.go](sources/manual_node.go), `sources/<cloud>/manual_node.go`) that feeds a synthetic inventory row through its own `createNodeFromResource`, and AWS re-keys by the picked VPC node's name the way `propagateVPCNamesToResources` does. The compulsory form fields are exactly what that cloud's `GenerateUniqueKey` reads (name, region, plus the VPC for EC2 / load balancers / RDS / Redshift). Kubernetes clusters do the same through `createNodeFromWorkload` (`sources/k8s/manual_node.go`: Deployment, StatefulSet, DaemonSet, CronJob, Service; key fields name + namespace; the cluster name is the account name, as the k8s source stamps it). `TestManualNode_<Cloud>KeyMatchesCollector` runs every declarable type through both paths (for K8s Services, the relay path too) and fails on any drift — if you change a `GenerateUniqueKey` or a VPC/naming pass, that test tells you whether declared nodes still merge.

- **Outside any connected account** a node is tenant-scoped (`external:<tenant>::<type>:<namespace>:<name>`, `cloud_account_id = tenant`, like the identity nodes). No collector can produce that key; it never merges.
- **Declarable types** are `core.ManualNodeTypes` (clouds and outside accounts) plus `core.ManualKubernetesNodeTypes` (Workload, K8sService; Kubernetes accounts only).
- **`markInactiveNodes` never sweeps `source = 'manual'`.** The Kubernetes types are infra-authoritative, so without that exclusion any build that did not re-emit a declared workload would retire it (`TestMarkInactiveNodes_NeverSweepsDeclaredNodes` fails both ways without it). The manual flow source's reconcile is the only thing that retires a declared node.
- **Writes are authorised per account.** Create, update, delete, Merge and every CSV row check `HasAccountAccess` on the declaration's account (handlers in `api/actions_kg_manual_nodes.go`); a declaration outside any connected account needs tenant-wide access, and so does Delete all.
- **Identity is immutable.** Account, type, name, region, VPC/namespace decide the id; edits change only description / schema properties / labels. Optional properties come from `core.SchemaProperties`: the type's `SpecificTypeSchema` minus key inputs (`core.IsManualReservedProperty`), fields only the collector can know — timestamps, runtime state, allocated IPs/ports, internal ids, derived flags (`core.IsManualObservedProperty`) — and lists of objects. A value must have the schema's `PropertyDef.Shape` (map / list / scalar), the shape the collector stores it in; declare `Shape` on any new non-scalar schema field.
- **The row stores the built node** (`kg_node_id`, `unique_key`, `base_properties`); `core.MaterializeManualNode` rebuilds the identical node at create time and every build.

**Per build** (`ManualNodeRepository.RunCycle`), before dependencies resolve:

1. **Adopt** — a declaration whose node a collector now writes (in this build's nodes, or in the graph with a non-manual source) is marked `adopted` and no longer emitted: in-build dedup lets later properties win, so emitting it would overwrite the collector's data, including the `source` the sweep reads. Adopted declarations are re-checked every build: when the collector no longer writes the node (the sweep retired it), the declaration is revived to `active` and emitted again.
2. **Emit** the rest, and add them to the node index the dependency resolver and `buildEdge` use. They are returned with the edges because `SaveEdges` drops edges whose endpoints are not in the build's node list.
3. **Reconcile** — retire `source='manual'` nodes with no live declaration. Needed because a delete that lands mid-build is undone by that build's `SaveNodes` (it writes every node active) and no sweep retires manual nodes.
4. **Flag possible duplicates** — a collected node with the same type + name (same account, or any account for a tenant-scoped declaration) under a different key. The UI offers **Merge**: pinned dependencies move to it, the declaration goes.

Declared `ExternalService` nodes are skipped by the ExternalService enricher and both collapse passes (`CollapseEnrichedExternalServices`, `ResolveGCPManagedServiceCalls`): they are what the user said they are, not observed stubs to fold into a cloud resource.

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

Exposed via RPC actions in [actions_knowledge_graph.go](../api/actions_knowledge_graph.go): `kg_get_complete_graph`, `kg_get_node`, `kg_get_edge`, `kg_get_filter_options`, `kg_get_node_neighbors`, `kg_list_path`, `kg_list_nodes`, `build_knowledge_graph`.

---

## Cross-Service Consumers

**Frontend** — [app/src/components1/KnowledgeGraph.jsx](../../../app/src/components1/KnowledgeGraph.jsx) (ReactFlow + ELK/d3-force, 1500-node cap), via [app/src/api1/kubernetes1/index.ts](../../../app/src/api1/kubernetes1/index.ts) `knowledgeGraph()` calling `kg_get_complete_graph`.

**Event investigation** — [core/action_service_map.go](core/action_service_map.go) auto-fetches a KG neighbourhood when an event has namespace+owner.

**LLM-server (SDG V2 agent)** — flag `llm_server_service_dependency_graph_v2_enabled` flips the `service_dependency_graph` agent from V1 (runtime-metric tool) to V2 (KG-only). V2 exposes:
- `kg_list_nodes` — discovery
- `kg_list_path` — directional BFS, returns `truncated`
- `kg_get_node` — drill-down (gated separately by `KGGetNodeEnabled` flag for payload control)
- `resource_search` — fuzzy name resolver

V2 declares the **ReAct** planner type (runs the ReAct3 engine). V1 and V2 are **mutually exclusive at init time** — when the flag is on, V1's metric-based runtime SDG tool is not registered.

---

## Things to Know Before Editing

- **Don't add `CREATE INDEX CONCURRENTLY` to KG migrations.** golang-migrate wraps each migration in a transaction by default — `CREATE INDEX CONCURRENTLY` cannot run inside one. Use plain `CREATE INDEX`, or add `-- migrate:no-transaction` at the top of the file. (See root [CLAUDE.md](../../../CLAUDE.md#database-migrations--rpc-actions).)
- **Migration timestamps must use current epoch ms** — `python3 -c "import time; print(int(time.time() * 1000))"`. Never hardcode.
- **`query_attributes` is driven by the per-`specific_type` schema** — when adding a new resource type, declare its `core.SpecificTypeSchema` co-located in `sources/<cloud>/<x>.go` (register via `init()`), marking filterable fields `Indexed: true` (they get hoisted) and identity fields `Required`. It's unioned with the per-NodeType `QueryablePropertiesMap` fallback in [core/types.go](core/types.go). A forcing-function test (`sources/<cloud>/*coverage_test.go`) fails if a new `specific_type` has no schema; a consistency test (`test/schema_consistency_test.go`) fails if the schema and the ontology mapping disagree on a property key. `PropertyDef.Name` must equal the exact `properties` key the extractor writes, or the field silently won't populate.
- **Edge priority matters for conflicts.** If two sources emit the same edge with different properties, the priority order in [flow_sources/edge_priority.go](flow_sources/edge_priority.go) decides who wins. Adding a new flow source means deciding where it slots in.
- **Flow sources cannot tombstone infra nodes.** `markInactiveNodes` respects `InfraAuthoritativeNodeTypes` — flow-source-only sync runs do not increment sync_version and do not delete infra.
- **Don't drop a signal just because you can't act on it.** If a classifier/matcher saw something suggestive but wasn't confident enough to decide, record it via `core.RecordUncertainClassification` instead of returning empty — see [Uncertain Classifications](#uncertain-classifications--the-near-miss-side-channel). It must stay fire-and-forget: short timeout off `context.Background()`, never the build's request context, and never a returned error.
- **The 1-hour per-tenant lock in the consumer is real** — a stuck or slow build blocks subsequent ones for that tenant for an hour.
- **Frontend caps at 1500 nodes** — bigger graphs need server-side filtering (account_ids, node_types) before the client gets them.

---

## Where to Read First, by Task

| Task | Files in order |
|---|---|
| Add a new node type | [core/types.go](core/types.go) (enum + `QueryablePropertiesMap` fallback) → relevant `sources/*_source.go` → declare `core.SpecificTypeSchema` co-located in the source file (drives `query_attributes`) + ontology spec in `core/ontology_data_<cloud>.go` → migration if backfill needed |
| Add a new source | [sources/interface.go](sources/interface.go) → [sources/registry.go](sources/registry.go) → copy structure of [sources/k8s_source.go](sources/k8s_source.go) |
| Add a new flow source | [flow_sources/interface.go](flow_sources/interface.go) → [flow_sources/base_flow_source.go](flow_sources/base_flow_source.go) → [flow_sources/edge_priority.go](flow_sources/edge_priority.go) (add to priority list) |
| Add a cross-account rule | [core/default_relationships.json](core/default_relationships.json) → [core/cross_account_relationships.go](core/cross_account_relationships.go) for matcher behaviour |
| Record a near-miss classification from a new source | [core/classification_review.go](core/classification_review.go) (recorder + candidate shape) → copy the producer→carrier→flush wiring in [traces/helpers.go](../traces/helpers.go) `detectApplicationType` and [flow_sources/traces_flow_source.go](flow_sources/traces_flow_source.go) `recordUncertainClassificationIfAny` → give each new guard its own `ReasonCode` |
| Debug a missing edge | confirm both endpoint nodes exist with correct unique_key → check source priority in [flow_sources/edge_priority.go](flow_sources/edge_priority.go) → check tombstone state (`is_active`) |
| Debug a stuck/slow build | [queue/consumer.go](queue/consumer.go) (lock state) → BuildGraphs phase logs in [core/service.go:570](core/service.go#L570) → batch sizes in SaveNodes/SaveEdges |
| Work on the LLM SDG V2 agent | [llm/llm-server/agents/agent_service_dependency_V2.go](../../../llm/llm-server/agents/agent_service_dependency_V2.go) → [tool_kg_traverse.go](../../../llm/llm-server/tools/tool_kg_traverse.go) → [TraverseDirectional:3463](core/service.go#L3463) |

---

## Tests

- Unit: `*_test.go` alongside source. Run with `make test` from `api-server/services`.
- E2E suite: [test/](test/) (`package test`) — Tier A golden diffs (JSON→graph, no DB, always run), Tier B DB-gated read/tombstone/orchestrator tests, and mocked-infra fake-collector tiers. Fixtures under [test/testdata/e2e/](test/testdata/). Run: `go test ./knowledge_graph/test/` from `api-server/services` (add `DATABASE_URL=...` to exercise Tier B; refresh goldens with `-update`).
- Integration: [test/integration_test.go](test/integration_test.go), `core/*_integration_test.go`. Some are gated by `TEST_ACCOUNT` env var (real cloud creds). See [TESTING.md](TESTING.md).
