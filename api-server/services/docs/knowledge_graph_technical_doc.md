# Knowledge Graph Architecture Documentation

## Overview

The Knowledge Graph system is a multi-source graph database that builds a unified view of infrastructure resources across AWS, Kubernetes, and observability platforms. It creates relationships between nodes (resources) and edges (connections) to enable service topology visualization and dependency mapping.

---

## High-Level Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        RPC ACTION HANDLERS                                  │
│                    (actions_knowledge_graph.go)                             │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         KNOWLEDGE GRAPH SERVICE                             │
│                            (core/service.go)                                │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                        BuildGraphs() Pipeline                          │ │
│  │                                                                        │ │
│  │   Phase 1 ──► Phase 2 ──► Phase 2.1 ──► Phase 2.5 ──► Phase 3        │ │
│  │   (Sources)  (Integrations) (Cross-Source) (Flow Sources) (Persist)  │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
              ┌───────────────────────┼───────────────────────┐
              │                       │                       │
              ▼                       ▼                       ▼
┌─────────────────────┐  ┌─────────────────────┐  ┌─────────────────────────┐
│  INFRASTRUCTURE     │  │   CROSS-SOURCE      │  │     FLOW SOURCES        │
│     SOURCES         │  │    ENRICHERS        │  │                         │
│  (sources/*.go)     │  │  (sources/*.go)     │  │  (flow_sources/*.go)    │
├─────────────────────┤  ├─────────────────────┤  ├─────────────────────────┤
│ • aws_source.go     │  │ • aws_lb_k8s_       │  │ • traces_flow_source    │
│ • k8s_source.go     │  │   enricher.go       │  │ • ebpf_flow_source      │
│                     │  │                     │  │ • datadog_apm_source    │
└─────────────────────┘  └─────────────────────┘  └─────────────────────────┘
         │                        │                        │
         ▼                        ▼                        ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                              UNIFIED GRAPH                                  │
│                        []*DbNode + []*DbEdge                                │
│                                                                             │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │  Deduplication → DeduplicateNodes() + DeduplicateEdgesWithPriority() │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          POSTGRESQL DATABASE                                │
│                                                                             │
│   ┌─────────────────────┐  ┌─────────────────────┐                         │
│   │  knowledge_graph_   │  │  knowledge_graph_   │                         │
│   │       nodes         │  │       edges         │                         │
│   └─────────────────────┘  └─────────────────────┘                         │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Graph Building Pipeline (BuildGraphs)

The `BuildGraphs()` method in [service.go](api-server/services/knowledge_graph/core/service.go) orchestrates the entire pipeline:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      BUILDGRAPHS PIPELINE PHASES                            │
└─────────────────────────────────────────────────────────────────────────────┘

  ┌────────────────┐     ┌────────────────┐     ┌────────────────────────────┐
  │   PHASE 1      │     │   PHASE 2      │     │      PHASE 2.1             │
  │   Account-     │ ──► │  Integration   │ ──► │   Cross-Source             │
  │   Specific     │     │   Graphs       │     │   Enrichment               │
  │   Graphs       │     │                │     │                            │
  └────────────────┘     └────────────────┘     └────────────────────────────┘
         │                      │                          │
         │                      │                          │
    AWS Source             Datadog                  aws_lb_k8s_enricher
    K8s Source            Integration              (LB → K8s Service)
         │                      │                          │
         ▼                      ▼                          ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │                        UNIFIED GRAPH (Nodes + Edges)                   │
  └────────────────────────────────────────────────────────────────────────┘
         │
         ▼
  ┌────────────────┐     ┌────────────────┐     ┌────────────────────────────┐
  │  PHASE 2.5     │     │   PHASE 3      │     │      POST-PROCESSING       │
  │    Flow        │ ──► │  Dedup &       │ ──► │   External Service         │
  │  Relationships │     │   Persist      │     │   Enrichment               │
  └────────────────┘     └────────────────┘     └────────────────────────────┘
         │
         │
    traces_flow_source
    ebpf_flow_source
    datadog_apm_source
```

### Phase Details

| Phase | Description | Location |
|-------|-------------|----------|
| **Phase 1** | Build graphs from infrastructure sources (AWS, K8s) per account | [service.go:550-670](api-server/services/knowledge_graph/core/service.go#L550-L670) |
| **Phase 2** | Build integration-specific graphs (Datadog) | [service.go:672-710](api-server/services/knowledge_graph/core/service.go#L672-L710) |
| **Phase 2.1** | Run cross-source enrichers (LB → K8s) | [service.go:714-747](api-server/services/knowledge_graph/core/service.go#L714-L747) |
| **Phase 2.5** | Build flow relationships from traces/eBPF | [service.go:749-788](api-server/services/knowledge_graph/core/service.go#L749-L788) |
| **Phase 3** | Deduplicate and persist to database | [service.go:790-799](api-server/services/knowledge_graph/core/service.go#L790-L799) |

---

## Source Types and Interfaces

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           SOURCE TYPE HIERARCHY                              │
└─────────────────────────────────────────────────────────────────────────────┘

  ┌───────────────────────────────────────────────────────────────────────────┐
  │                          SourceInterface                                  │
  │                         (core/service.go:18-24)                           │
  ├───────────────────────────────────────────────────────────────────────────┤
  │  GetName() string                                                         │
  │  BuildGraph(ctx, req) (*Graph, error)                                     │
  │  IsEnabled() bool                                                         │
  │  Validate() error                                                         │
  └───────────────────────────────────────────────────────────────────────────┘
                     │                                    │
                     ▼                                    ▼
     ┌───────────────────────────────┐    ┌───────────────────────────────────┐
     │        AWSSource              │    │         K8sSource                 │
     │    (sources/aws_source.go)    │    │    (sources/k8s_source.go)        │
     ├───────────────────────────────┤    ├───────────────────────────────────┤
     │  Creates:                     │    │  Creates:                         │
     │  • LoadBalancer               │    │  • Cluster, Namespace             │
     │  • BackendPool (Target Group) │    │  • Workload, K8sService           │
     │  • VPC, Subnet, SG            │    │  • Pod, Node, Ingress             │
     │  • RDS, ElastiCache, SQS/SNS  │    │  • PVC, PV, ConfigMap             │
     │  • Route53 zones/records      │    │  • HelmRelease                    │
     └───────────────────────────────┘    └───────────────────────────────────┘


  ┌───────────────────────────────────────────────────────────────────────────┐
  │                     FlowSourceInterface                                   │
  │                  (flow_sources/interface.go:22-40)                        │
  ├───────────────────────────────────────────────────────────────────────────┤
  │  GetName() string                                                         │
  │  BuildFlowRelationships(ctx, req) ([]*DbEdge, []*DbNode, error)           │
  │  IsEnabled() bool                                                         │
  │  GetSourceCategory() FlowSourceCategory                                   │
  └───────────────────────────────────────────────────────────────────────────┘
          │                    │                        │
          ▼                    ▼                        ▼
  ┌────────────────┐  ┌────────────────┐  ┌──────────────────────────┐
  │ TracesFlowSrc  │  │  eBPFFlowSrc   │  │  DatadogAPMFlowSrc       │
  └────────────────┘  └────────────────┘  └──────────────────────────┘
  Category: tracing   Category: networking  Category: tracing
  Creates CALLS edges Creates CALLS edges   Creates CALLS edges


  ┌───────────────────────────────────────────────────────────────────────────┐
  │                  CrossSourceEnricherInterface                             │
  │                       (core/service.go:58-69)                             │
  ├───────────────────────────────────────────────────────────────────────────┤
  │  GetName() string                                                         │
  │  EnrichCrossSources(ctx, nodes, edges, tenantID) (nodes, edges, error)    │
  └───────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
                    ┌───────────────────────────────────┐
                    │    LoadBalancerK8sEnricher        │
                    │  (sources/aws_lb_k8s_enricher.go) │
                    ├───────────────────────────────────┤
                    │  Creates edges:                   │
                    │  LB ──ROUTES_TO──► K8s Service    │
                    │  LB ──ROUTES_TO──► Workload       │
                    └───────────────────────────────────┘
```

---

## Node Types and Categories

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         NODE TYPE CATEGORIES                                 │
│                        (core/types.go:10-73)                                 │
└─────────────────────────────────────────────────────────────────────────────┘

  ┌─────────────────────────────────────────────────────────────────────────┐
  │                    NON-INFRASTRUCTURE NODES                              │
  │                    (Application Layer)                                   │
  ├─────────────────────────────────────────────────────────────────────────┤
  │  NodeTypeService            │  Service endpoint discovered from traces  │
  │  NodeTypeWorkload           │  Deployment/StatefulSet/DaemonSet         │
  │  NodeTypeExternalService    │  External dependencies (3rd party APIs)   │
  │  NodeTypeServerlessFunction │  Lambda, Cloud Functions                  │
  └─────────────────────────────────────────────────────────────────────────┘

  ┌─────────────────────────────────────────────────────────────────────────┐
  │                      INFRASTRUCTURE NODES                                │
  ├─────────────────────────────────────────────────────────────────────────┤
  │  KUBERNETES                  │  CLOUD RESOURCES                          │
  │  ─────────────────────────── │  ────────────────────────────────────────│
  │  Cluster, Namespace          │  LoadBalancer, BackendPool               │
  │  Pod, Node, K8sService       │  VPC, Subnet, SecurityGroup              │
  │  Ingress, NetworkPolicy      │  Database, Cache, MessageQueue           │
  │  PVC, PV, ConfigMap          │  Storage, ContainerRegistry              │
  │  HelmChart, HelmRelease      │  DNSZone, DNSRecord, CDN                 │
  │                              │  NetworkGateway, PublicIP                │
  └─────────────────────────────────────────────────────────────────────────┘
```

---

## Unique Key Format (6-Part Structure)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      UNIQUE KEY STRUCTURE                                    │
│                   (core/unique_key_builder.go)                              │
└─────────────────────────────────────────────────────────────────────────────┘

  Format: {source}:{account}:{location}:{NodeType}:{hierarchy}:{name}

  ┌─────────┬───────────────────────────────────────────────────────────────┐
  │ Part    │ Description                                                   │
  ├─────────┼───────────────────────────────────────────────────────────────┤
  │ source  │ aws, azure, k8s, gcp, ebpf, trace, cloud                      │
  │ account │ Account ID, subscription ID, cluster ID, project ID          │
  │ location│ Region, zone, AZ (empty if not applicable)                   │
  │ NodeType│ LoadBalancer, Workload, K8sService, etc.                      │
  │hierarchy│ VPC, resource group, namespace (empty if not applicable)     │
  │ name    │ Resource name or identifier                                   │
  └─────────┴───────────────────────────────────────────────────────────────┘

  Examples:
  ┌────────────────────────────────────────────────────────────────────────┐
  │ aws:acc123:us-east-1:LoadBalancer:vpc-abc:my-alb                       │
  │ k8s:cluster-456::Workload:production:order-service                     │
  │ k8s:cluster-456::K8sService:production:order-service                   │
  │ ebpf:acc789::Service::payment-gateway                                  │
  └────────────────────────────────────────────────────────────────────────┘
```

---

## Edge Types (Relationships)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        RELATIONSHIP TYPES                                    │
│                        (core/types.go)                                       │
└─────────────────────────────────────────────────────────────────────────────┘

  CONNECTIVITY RELATIONSHIPS:
  ┌────────────────────────────────────────────────────────────────────────┐
  │  CALLS              │  Service A ──CALLS──► Service B                  │
  │  RESOLVES_TO        │  ExternalService ──RESOLVES_TO──► CloudResource  │
  │  ROUTES_TO          │  LoadBalancer ──ROUTES_TO──► K8sService          │
  │  ROUTES_TO_BACKEND  │  LoadBalancer ──ROUTES_TO_BACKEND──► TargetGroup │
  │  ROUTES_TO_SERVICE  │  Ingress ──ROUTES_TO_SERVICE──► K8sService       │
  │  ROUTES_THROUGH     │  K8sService ──ROUTES_THROUGH──► Ingress          │
  └────────────────────────────────────────────────────────────────────────┘

  MESSAGING RELATIONSHIPS:
  ┌────────────────────────────────────────────────────────────────────────┐
  │  PUBLISHES_TO       │  Service ──PUBLISHES_TO──► Topic/Queue           │
  │  SUBSCRIBES_TO      │  Service ──SUBSCRIBES_TO──► Topic/Queue          │
  └────────────────────────────────────────────────────────────────────────┘

  HIERARCHICAL RELATIONSHIPS:
  ┌────────────────────────────────────────────────────────────────────────┐
  │  CONTAINS           │  Cluster ──CONTAINS──► Namespace                 │
  │  RUNS_IN            │  Workload ──RUNS_IN──► Namespace                 │
  │  BELONGS_TO         │  Pod ──BELONGS_TO──► Workload                    │
  │  USES               │  Workload ──USES──► ConfigMap                    │
  └────────────────────────────────────────────────────────────────────────┘
```

---

## Edge Priority System

When multiple sources create the same edge (same source node → dest node), priority determines which source is primary:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      EDGE SOURCE PRIORITY                                    │
│                     (core/helpers.go:146-224)                                │
└─────────────────────────────────────────────────────────────────────────────┘

  Priority: 1 (highest) → 6 (lowest)

  For CALLS relationships:
  ┌─────────────────────────────────────────────────────────────────────────┐
  │  Priority 1: k8s          │  K8s has authoritative service data        │
  │  Priority 2: aws          │  AWS has authoritative cloud data          │
  │  Priority 3: ebpf         │  eBPF has accurate network-level data      │
  │  Priority 4: traces       │  Traces has rich application data          │
  │  Priority 5: datadog-apm  │  External APM source                       │
  │  Priority 6: (unknown)    │  Default lowest priority                   │
  └─────────────────────────────────────────────────────────────────────────┘

  Deduplication Process (DeduplicateEdgesWithPriority):
  ┌────────────────────────────────────────────────────────────────────────┐
  │  1. Create composite key: sourceNodeID:destNodeID:tenantID             │
  │  2. If edge exists, compare priorities                                 │
  │  3. Higher priority source becomes primary                             │
  │  4. Lower priority metrics prefixed: traces_latency_ms                 │
  │  5. Track all sources in "contributing_sources" property               │
  └────────────────────────────────────────────────────────────────────────┘
```

---

## Global Registry Pattern

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    GLOBAL SOURCE REGISTRY                                    │
│                    (sources/registry.go)                                     │
└─────────────────────────────────────────────────────────────────────────────┘

                      ┌─────────────────────────────┐
                      │    Source Factory           │
                      │    Registry (map)           │
                      │    + Mutex                  │
                      └─────────────────────────────┘
                                   │
         ┌─────────────────────────┼─────────────────────────┐
         ▼                         ▼                         ▼
  ┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
  │  aws_source     │     │  k8s_source     │     │ aws_lb_k8s      │
  │  factory        │     │  factory        │     │ enricher factory│
  └─────────────────┘     └─────────────────┘     └─────────────────┘
         │                         │                         │
         └─────────────────────────┼─────────────────────────┘
                                   ▼
                        init() function calls:
                        RegisterSourceFactory(name, factory, description)
                        RegisterCrossSourceEnricherFactory(name, factory, desc)


  Registration Flow:
  ┌────────────────────────────────────────────────────────────────────────┐
  │  1. Each source has init() that registers its factory                  │
  │  2. RegisterAllSourcesToService() iterates registry                    │
  │  3. CreateSource() calls factory to instantiate source                 │
  │  4. Service.RegisterSource() adds to s.sources map                     │
  └────────────────────────────────────────────────────────────────────────┘
```

---

## Data Flow: LoadBalancer → K8s Service

```
┌─────────────────────────────────────────────────────────────────────────────┐
│           CROSS-SOURCE ENRICHMENT: AWS LB → K8S SERVICE                     │
│                  (aws_lb_k8s_enricher.go)                                    │
└─────────────────────────────────────────────────────────────────────────────┘

  ┌───────────────────────────────────────────────────────────────────────────┐
  │                       MATCHING STRATEGIES                                  │
  └───────────────────────────────────────────────────────────────────────────┘

  Strategy 1: K8s Tags Matching
  ┌────────────────────────────────────────────────────────────────────────┐
  │  AWS LB Tags:                        K8s Service Labels:              │
  │  kubernetes.io/service-name  ───────► name                            │
  │  kubernetes.io/namespace     ───────► namespace                       │
  │  kubernetes.io/cluster/xyz   ───────► cluster                         │
  └────────────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
  Strategy 2: NodePort Matching
  ┌────────────────────────────────────────────────────────────────────────┐
  │  LB Listener Port  ───────► K8s Service NodePort                       │
  └────────────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
  Strategy 3: Target IP → cluster_ip
  ┌────────────────────────────────────────────────────────────────────────┐
  │  Target Group Target IPs  ───────► K8s Service cluster_ip              │
  └────────────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
  Strategy 4: Prometheus Pod Resolution (NEW)
  ┌────────────────────────────────────────────────────────────────────────┐
  │  Target IP → kube_pod_info → Pod → created_by_kind/name               │
  │           → kube_replicaset_owner → Deployment                        │
  │           → Find existing node OR create new Workload                  │
  └────────────────────────────────────────────────────────────────────────┘


  ┌───────────────────────────────────────────────────────────────────────────┐
  │                         RESULT                                             │
  └───────────────────────────────────────────────────────────────────────────┘

  ┌────────────────┐                      ┌────────────────┐
  │  AWS           │   ROUTES_TO          │  K8s           │
  │  LoadBalancer  │ ──────────────────►  │  Service       │
  │  (my-alb)      │                      │  (order-svc)   │
  └────────────────┘                      └────────────────┘
```

---

## Node & Edge Creation Helpers

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     NODE/EDGE CREATION PATTERN                               │
│                        (core/helpers.go)                                     │
└─────────────────────────────────────────────────────────────────────────────┘

  Step 1: Create UniqueKeyComponents
  ┌────────────────────────────────────────────────────────────────────────┐
  │  keyComponents := core.NewUniqueKeyComponents("k8s", NodeTypeWorkload) │
  │  keyComponents.Account = k8sAccountID                                  │
  │  keyComponents.Hierarchy = namespace                                   │
  │  keyComponents.Name = "order-service"                                  │
  │  uniqueKey := keyComponents.Build()                                    │
  │  // Result: k8s:cluster-123::Workload:production:order-service         │
  └────────────────────────────────────────────────────────────────────────┘

  Step 2: Create Node using core.NewNode()
  ┌────────────────────────────────────────────────────────────────────────┐
  │  node := core.NewNode(                                                 │
  │      core.NodeTypeWorkload,  // NodeType                               │
  │      uniqueKey,              // Generated unique key                   │
  │      properties,             // map[string]interface{}                 │
  │      tenantID,               // Tenant identifier                      │
  │      cloudAccountID,         // Account ID                             │
  │      "k8s",                  // Source name                            │
  │  )                                                                     │
  │                                                                        │
  │  // NewNode internally:                                                │
  │  // - Generates ID: GenerateNodeID(uniqueKey + tenantID + accountID)  │
  │  // - Sets CreatedAt, UpdatedAt timestamps                             │
  │  // - Extracts Labels from properties                                  │
  └────────────────────────────────────────────────────────────────────────┘

  Step 3: Create Edge using core.NewEdge()
  ┌────────────────────────────────────────────────────────────────────────┐
  │  edge := core.NewEdge(                                                 │
  │      sourceNode.ID,          // Use .ID, not .UniqueKey                │
  │      destNode.ID,            // Use .ID, not .UniqueKey                │
  │      core.RelationshipRoutesTo, // Relationship type                   │
  │      properties,             // Edge properties                        │
  │      tenantID,                                                         │
  │      cloudAccountID,                                                   │
  │      "aws_lb_k8s_enricher",  // Source name                           │
  │  )                                                                     │
  │                                                                        │
  │  // NewEdge internally:                                                │
  │  // - Generates ID: GenerateEdgeID(srcID, destID, relType)            │
  │  // - Sets CreatedAt, UpdatedAt timestamps                             │
  └────────────────────────────────────────────────────────────────────────┘
```

---

## Service Struct Components

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          SERVICE STRUCT                                      │
│                       (core/service.go:71-82)                                │
└─────────────────────────────────────────────────────────────────────────────┘

  type Service struct {
      logger                    *slog.Logger
      sources                   map[string]SourceInterface      // AWS, K8s
      flowSources               map[string]FlowSourceInterface  // Traces, eBPF
      externalServiceEnricher   ExternalServiceEnricherInterface
      crossSourceEnrichers      []CrossSourceEnricherInterface  // LB→K8s
      dbManager                 *database.DatabaseManager
      defaultRelationshipsPath  string
      defaultRelationshipsCache []CrossAccountRelationship
      ctx                       *security.RequestContext
  }

  Registration Methods:
  ┌────────────────────────────────────────────────────────────────────────┐
  │  RegisterSource(SourceInterface)                                       │
  │  RegisterFlowSource(FlowSourceInterface)                               │
  │  RegisterCrossSourceEnricher(CrossSourceEnricherInterface) ← FIXED    │
  │  SetExternalServiceEnricher(ExternalServiceEnricherInterface)         │
  └────────────────────────────────────────────────────────────────────────┘
```

---

## Key Files Reference

| Component | File | Description |
|-----------|------|-------------|
| **Core Service** | [service.go](api-server/services/knowledge_graph/core/service.go) | Main service, BuildGraphs pipeline |
| **Types** | [types.go](api-server/services/knowledge_graph/core/types.go) | NodeType, RelationshipType definitions |
| **Helpers** | [helpers.go](api-server/services/knowledge_graph/core/helpers.go) | NewNode, NewEdge, deduplication |
| **Unique Keys** | [unique_key_builder.go](api-server/services/knowledge_graph/core/unique_key_builder.go) | 6-part key format |
| **AWS Source** | [aws_source.go](api-server/services/knowledge_graph/sources/aws_source.go) | AWS resource graph |
| **K8s Source** | [k8s_source.go](api-server/services/knowledge_graph/sources/k8s_source.go) | Kubernetes resource graph |
| **Registry** | [registry.go](api-server/services/knowledge_graph/sources/registry.go) | Global factory registry |
| **LB Enricher** | [aws_lb_k8s_enricher.go](api-server/services/knowledge_graph/sources/aws_lb_k8s_enricher.go) | Cross-source enrichment |
| **Flow Interface** | [interface.go](api-server/services/knowledge_graph/flow_sources/interface.go) | Flow source interface |
| **Traces Flow** | [traces_flow_source.go](api-server/services/knowledge_graph/flow_sources/traces_flow_source.go) | Trace-based edges |
| **eBPF Flow** | [ebpf_flow_source.go](api-server/services/knowledge_graph/flow_sources/ebpf_flow_source.go) | Network-level edges |

---

## Summary

The Knowledge Graph architecture follows a **pipeline pattern** with clear phases:

1. **Infrastructure Sources** (AWS, K8s) → Create resource nodes
2. **Cross-Source Enrichers** → Link resources across accounts
3. **Flow Sources** (Traces, eBPF) → Add connectivity edges
4. **Deduplication** → Merge duplicates with priority
5. **Persistence** → Store to PostgreSQL

Each component follows consistent patterns for node/edge creation using `core.NewNode()` and `core.NewEdge()` helpers, and the 6-part unique key format ensures consistent identification across sources.
