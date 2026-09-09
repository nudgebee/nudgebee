package core

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lib/pq"
)

// CoverageConfidence expresses how much to trust an ImpactSummary, so callers
// never mistake "we couldn't observe dependents" for "there are none". Deriving a
// user-facing safety band from a summary is the recommendation layer's job; the
// graph core stays policy-free on purpose.
type CoverageConfidence string

const (
	// CoverageNone — the resource was not found in the graph; impact is unknown
	// (must not be read as "safe").
	CoverageNone CoverageConfidence = "none"
	// CoverageLow — the resource is present, but nothing establishes an active
	// signal was watching: dependents (if any) came from a single source and no
	// flow evidence touches the resource or its account. Absence of evidence.
	CoverageLow CoverageConfidence = "low"
	// CoverageObserved — no multi-source corroboration, but the resource sits
	// inside an active traffic signal's watched perimeter (eBPF / traces / APM
	// asserted an edge touching it, or produced edges in its cloud account this
	// build). The signal's silence about the resource is evidence of absence.
	CoverageObserved CoverageConfidence = "observed"
	// CoverageHigh — at least one dependency edge is corroborated by multiple
	// discovery sources (e.g. traces + Datadog).
	CoverageHigh CoverageConfidence = "high"
)

// flowObservationSources are the discovery sources that constitute active
// traffic observation (as opposed to declared/structural metadata) — the
// signals whose silence about a resource is meaningful.
var flowObservationSources = map[string]bool{
	"ebpf":             true,
	"traces":           true,
	"gcp-cloud-traces": true,
	"datadog-apm":      true,
	"newrelic-apm":     true,
}

// ImpactedService is one dependent of a resource: an application-level node that
// relies on it and could be affected if the resource is rightsized or removed.
type ImpactedService struct {
	NodeID   string   `json:"node_id"`
	Name     string   `json:"name"`
	NodeType NodeType `json:"node_type"`
	// ResourceID is the provider's own id for the node (an EC2 instance id, an
	// ARN tail). Cloud alarms name their subject by it — a CPU alarm's subject is
	// "i-0f568ef22d52139bb" — while the graph node is named by its Name tag, so
	// callers matching alerts to dependents need both spellings or the two never
	// meet. Empty for nodes that have no provider id (every Kubernetes one).
	ResourceID  string `json:"resource_id,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Environment string `json:"environment,omitempty"`
	HopsAway    int    `json:"hops_away"`
	// Relationship is the edge type linking this node one hop toward the seed
	// (its own edge when direct, its first walked edge when multi-hop); Sources
	// is the union of discovery sources asserting any such edge — the provenance
	// behind the dependency claim.
	Relationship RelationshipType `json:"relationship,omitempty"`
	Sources      []string         `json:"sources,omitempty"`
	// PodCount is set only on HostedWorkloads entries: how many of the
	// workload's pods are scheduled on the seed instance/node right now.
	PodCount int `json:"pod_count,omitempty"`
}

// ImpactSummary is the blast-radius rollup for a single resource node.
type ImpactSummary struct {
	SeedNodeID           string             `json:"seed_node_id"`
	SeedNodeType         NodeType           `json:"seed_node_type"`
	DependentCount       int                `json:"dependent_count"` // application-level dependents only
	ProductionDependents int                `json:"production_dependents"`
	DependentsByType     map[NodeType]int   `json:"dependents_by_type"`
	Dependents           []ImpactedService  `json:"dependents"`
	CoverageConfidence   CoverageConfidence `json:"coverage_confidence"`
	Truncated            bool               `json:"truncated"`
	// EnvironmentResolved marks that per-dependent environments were resolved
	// against the tenant's account tiers (cloud_accounts.account_env), so
	// ProductionDependents == 0 is a real "nothing production" claim. False on
	// summaries persisted before environment resolution existed, and when the
	// account lookup failed — consumers must render those as "environment
	// unknown", not as a verified zero.
	EnvironmentResolved bool `json:"environment_resolved"`
	// DownstreamDependencies is the reverse direction: what the seed itself
	// calls, publishes to, or subscribes to (one hop). Operator context —
	// deliberately excluded from DependentCount and the safety band, which
	// grade risk to callers only.
	DownstreamDependencies []ImpactedService `json:"downstream_dependencies,omitempty"`
	DownstreamCount        int               `json:"downstream_count,omitempty"`
	// InfrastructureDependents are traversed dependents that are not
	// application-level types — the intermediates DependentCount deliberately
	// omits (a Node, a Namespace, a PV). They are reported because "not a service
	// that breaks" is a Kubernetes-shaped judgement: on a VM deployment the
	// ComputeInstance calling a database *is* the application, so a blast radius
	// that hid it would read as "nothing impacted" when something plainly is.
	//
	// Kept out of DependentCount, ProductionDependents and the safety band on
	// purpose — those grade risk to application callers, and widening them would
	// change FinOps recommendation scoring for every tenant.
	InfrastructureDependents []ImpactedService `json:"infrastructure_dependents,omitempty"`
	InfrastructureCount      int               `json:"infrastructure_count,omitempty"`
	// HostedWorkloads are the workloads scheduled on a ComputeInstance/Node
	// seed, rolled up from live pod placement (k8s_pods) with per-workload pod
	// counts. Deliberately a separate list: a hosted workload reschedules when
	// its node changes — it is not a caller that breaks — so folding it into
	// DependentCount would grade every node rec on a prod account risky and
	// repeal the callers-only contract above. The safety band consults the
	// count only for destructive changes (removal strands what is scheduled
	// here); see recommendation/safety_band.go and docs/architecture-decisions.md.
	HostedWorkloads     []ImpactedService `json:"hosted_workloads,omitempty"`
	HostedWorkloadCount int               `json:"hosted_workload_count,omitempty"`
}

// impactRelationshipDefaults maps a resource node type to the relationship types
// whose source is the dependent. Traversal is always upstream (destination →
// source), so for a Database (Service CALLS Database) the upstream neighbour is
// the calling Service; for a ComputeInstance (Node/Pod RUNS_ON Instance) it is
// the hosted workloads, etc. Callers may override.
var impactRelationshipDefaults = map[NodeType][]RelationshipType{
	NodeTypeDatabase: {RelationshipCalls},
	NodeTypeCache:    {RelationshipCalls},
	// Storage lists the whole k8s consumption chain, not just the first hop:
	// the relationship filter re-applies at every BFS level, so reaching the
	// mounting workload (Storage ← PROVIDES_STORAGE ← PV ← IS_BOUND_TO ← PVC
	// ← MOUNTS ← Workload) needs all three k8s edge types listed here — and
	// impactDepthDefaults gives Storage seeds the three levels the chain spans.
	NodeTypeStorage:      {RelationshipCalls, RelationshipProvidesStorage, RelationshipIsBoundTo, RelationshipMounts},
	NodeTypeMessageQueue: {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	NodeTypeQueue:        {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	NodeTypeTopic:        {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	// CALLS on an instance is the VM-as-application case: on a VM stack the
	// meaningful dependency is whoever talks to the machine (VPC flow logs /
	// eBPF), not a workload layer that doesn't exist. App-typed callers count
	// as dependents; instance-typed callers (sibling VMs, k8s node ENI noise)
	// are not app types and therefore surface in InfrastructureDependents
	// with hop distance, never in the band.
	NodeTypeComputeInstance: {RelationshipRunsOn, RelationshipManages, RelationshipOwns, RelationshipCalls},
	NodeTypeNode:            {RelationshipRunsOn, RelationshipManages, RelationshipOwns},
	NodeTypePV:              {RelationshipProvidesStorage, RelationshipIsBoundTo, RelationshipMounts},
	NodeTypePVC:             {RelationshipMounts, RelationshipIsBoundTo},
	NodeTypeK8sService:      {RelationshipExposes, RelationshipRoutesToService},
	NodeTypeLoadBalancer:    {RelationshipRoutesToBackend, RelationshipRoutesToService},
	NodeTypeWorkload:        {RelationshipCalls},
	NodeTypeService:         {RelationshipCalls},
}

// notImpactableTypes are node types that can be attached to a resource but can
// never be *impacted* by it failing. An instance's inbound edges include its
// owner (OWNS, from the ownership enricher) and the IaC stack that declares it
// (MANAGES), and both were reported as dependents — a live blast radius listed
// a person and a CloudFormation stack under "calls this directly", beside the
// two instances that genuinely do.
//
// Filtered by node type rather than by relationship: OWNS and MANAGES are
// meaningful inbound edges for a Kubernetes Node, where they reach the pods it
// runs. It is the destination type that is wrong here, not the edge.
//
// These stay in DependentsByType — the ownership and stack links are real and
// worth knowing — they are simply not blast radius.
var notImpactableTypes = map[NodeType]bool{
	NodeTypeUserAccount:     true, // a human owner
	NodeTypeUserGroup:       true, // an owning team
	NodeTypeInfraStack:      true, // the CloudFormation/Terraform stack that declares it
	NodeTypeServiceIdentity: true, // the IAM role it assumes
}

func canBeImpacted(t NodeType) bool { return !notImpactableTypes[t] }

// impactDepthDefaults overrides the default traversal depth (2) per seed type,
// used when the caller passes maxDepth <= 0. Storage needs three levels to
// cross PV and PVC before reaching the mounting workload.
var impactDepthDefaults = map[NodeType]int{
	NodeTypeStorage: 3,
}

// ImpactSeedNodeTypes returns the node types that have a defined blast-radius
// traversal, sorted for deterministic iteration.
//
// This is the authoritative answer to "what can be a blast-radius seed": a type
// is listed in impactRelationshipDefaults precisely because traversing from it
// means something. Plumbing (SecurityGroup, Subnet, VPC) is absent by design —
// seeding on a security group would report every instance attached to it as
// impacted. Callers that need to resolve an event onto a seed node should derive
// their candidate types from here rather than keeping a parallel list, so adding
// a traversal above is enough to make that type resolvable.
// The order is computed once: impactRelationshipDefaults is static, and callers
// sit on the event-resolution path. A copy is returned because the type is
// exported — a caller that sorted or truncated the shared slice would corrupt
// every later resolution.
var impactSeedNodeTypes = func() []NodeType {
	out := make([]NodeType, 0, len(impactRelationshipDefaults))
	for nodeType := range impactRelationshipDefaults {
		out = append(out, nodeType)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}()

func ImpactSeedNodeTypes() []NodeType {
	out := make([]NodeType, len(impactSeedNodeTypes))
	copy(out, impactSeedNodeTypes)
	return out
}

// fallbackImpactRelationships is used when the seed's node type has no specific
// mapping — the common dependency edges without traversing noise.
var fallbackImpactRelationships = []RelationshipType{
	RelationshipCalls, RelationshipRunsOn, RelationshipMounts,
}

// downstreamRelationshipDefaults maps seed node types to the edge types whose
// source is the seed — what the seed itself depends on. Only application seeds
// get a downstream pass: for data-plane resources (databases, queues, compute)
// the blast radius is their callers, and what they depend on adds no context
// to a change recommendation.
var downstreamRelationshipDefaults = map[NodeType][]RelationshipType{
	NodeTypeWorkload:           {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	NodeTypeService:            {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	NodeTypePod:                {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	NodeTypeJob:                {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	NodeTypeCronJob:            {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	NodeTypeServerlessFunction: {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	// A load balancer is the one seed whose useful neighbourhood is entirely
	// downstream: nothing routes *to* it (its clients are outside the graph),
	// while everything it fronts hangs off its outgoing edges. Without this a
	// load-balancer alarm reports no topology at all — the upstream pass returns
	// nothing by construction.
	//
	// RelationshipRoutesTo is listed alongside the two K8s-shaped ingress types
	// because it is the edge AWS actually emits for ALB/NLB → EC2 target-group
	// membership (see sources/aws/loadbalancer.go buildLBTargetEdges); the
	// ROUTES_TO_BACKEND / ROUTES_TO_SERVICE pair alone never matches a cloud
	// load balancer.
	//
	// Routing edges only — deliberately NOT RelationshipCalls. Continuing the
	// walk through the backend's own traffic looks appealing (it would name the
	// tier behind the front door) but a load balancer's backend calls back
	// through the balancer's own ENIs, which arrive as unresolved ExternalService
	// IP nodes. Those are an app-level type, so they survive the
	// downstreamDependencyTypes filter and the panel ends up reporting that the
	// load balancer depends on its own two private IPs. The backend is one click
	// away and its panel tells the rest of the story correctly.
	NodeTypeLoadBalancer: {RelationshipRoutesTo, RelationshipRoutesToBackend, RelationshipRoutesToService},
}

// downstreamDependencyTypes are the node types worth naming as something the
// seed depends on: the application set plus the data-plane resources a
// workload commonly calls into.
var downstreamDependencyTypes = func() map[NodeType]bool {
	m := map[NodeType]bool{
		NodeTypeDatabase:     true,
		NodeTypeCache:        true,
		NodeTypeMessageQueue: true,
		NodeTypeQueue:        true,
		NodeTypeTopic:        true,
		NodeTypeStorage:      true,
		// A VM is a dependency worth naming, not plumbing: on a cloud stack the
		// instance a load balancer fronts *is* the tier that serves the request,
		// and omitting it leaves an ALB alarm reporting nothing it depends on.
		// Safe to widen here because this set gates only what gets *named* as a
		// downstream dependency — DownstreamDependencies never feeds
		// DependentCount or ProductionDependents (the upstream/dependent side
		// that does uses appDependentTypes, deliberately left alone). The one
		// band interaction is deliberate and count-only: a DESTRUCTIVE change
		// refuses to soften while DownstreamCount > 0 (see safety_band.go and
		// docs/architecture-decisions.md).
		NodeTypeComputeInstance: true,
		// K8sService is the most common landing type for an AWS-LB → EKS
		// routing edge (aws_lb_k8s_enricher and both cross-account LB rules
		// target it); without it an ALB fronting a cluster names nothing at
		// all. Same band-safety argument as ComputeInstance above.
		NodeTypeK8sService: true,
	}
	for t := range appDependentTypes {
		m[t] = true
	}
	return m
}()

// appDependentTypes are the node types that count as an application-level
// dependent for the headline blast-radius count. Infrastructure intermediates
// (Node, Namespace, PV) may be traversed to reach these but are not themselves
// "services that break".
var appDependentTypes = map[NodeType]bool{
	NodeTypeService:            true,
	NodeTypeWorkload:           true,
	NodeTypePod:                true,
	NodeTypeServerlessFunction: true,
	NodeTypeExternalService:    true,
	NodeTypeJob:                true,
	NodeTypeCronJob:            true,
}

// defaultImpactRelationshipStrings returns the default dependency relationship
// types for a seed node type, as strings for the traversal API.
func defaultImpactRelationshipStrings(nodeType NodeType) []string {
	rels, ok := impactRelationshipDefaults[nodeType]
	if !ok {
		rels = fallbackImpactRelationships
	}
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = string(r)
	}
	return out
}

// downstreamRelationshipStrings returns the downstream relationship types for
// a seed node type, empty when the type gets no downstream pass.
func downstreamRelationshipStrings(nodeType NodeType) []string {
	rels := downstreamRelationshipDefaults[nodeType]
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = string(r)
	}
	return out
}

// maxImpactNodes caps how many discovered nodes a single impact traversal
// processes (either direction); the upstream pass reports overflow via
// Truncated.
const maxImpactNodes = 500

// GetImpactedServices computes the blast radius of a resource node: the
// application-level dependents that rely on it and could be affected if it is
// rightsized, moved, or removed. It is the structural primitive behind
// recommendation safety scoring — core returns dependents plus an honest coverage
// signal, and the recommendation layer derives the user-facing safety band from
// it (kept out of core on purpose).
//
// Traversal is always upstream: a dependent is the source of a CALLS / RUNS_ON /
// MOUNTS / ... edge whose destination is the resource. relationshipTypes defaults
// per seed type when empty; maxDepth defaults to 2 and is clamped to 3.
// Application seeds additionally get a one-hop downstream pass (what the seed
// itself calls / publishes to / subscribes to), surfaced as
// DownstreamDependencies — context that never feeds DependentCount or the
// safety band.
//
// Known limitation (tracked for follow-up): reaching workloads behind a
// ComputeInstance/Node depends on persisted RUNS_ON/OWNS edges; synthesized pods
// are not walked here, so ComputeInstance coverage can be partial — which the
// CoverageConfidence signal reflects rather than hides.
func (s *Service) GetImpactedServices(tenantID, nodeID string, relationshipTypes []string, maxDepth int) (*ImpactSummary, error) {
	if tenantID == "" || nodeID == "" {
		return nil, fmt.Errorf("tenantID and nodeID are required")
	}
	// Depth defaulting is seed-aware (impactDepthDefaults), so it waits for the
	// seed fetch below; the clamp applies to caller-supplied values right away.
	if maxDepth > 3 {
		maxDepth = 3
	}

	// Resolve the seed first: an absent node means impact is unknown, not zero —
	// callers must not read that as "safe".
	seedNodes, err := s.fetchNodesByIDs([]string{nodeID})
	if err != nil {
		return nil, fmt.Errorf("fetch seed node %s: %w", nodeID, err)
	}
	if len(seedNodes) == 0 {
		return &ImpactSummary{
			SeedNodeID:         nodeID,
			DependentsByType:   map[NodeType]int{},
			Dependents:         []ImpactedService{},
			CoverageConfidence: CoverageNone,
		}, nil
	}
	seed := seedNodes[0]

	// Defense in depth: the traversal helpers resolve by globally-unique node ID,
	// but enforce the caller's tenant explicitly so this entry point can never
	// surface another tenant's resource.
	if seed.TenantID != tenantID {
		return &ImpactSummary{
			SeedNodeID:         nodeID,
			DependentsByType:   map[NodeType]int{},
			Dependents:         []ImpactedService{},
			CoverageConfidence: CoverageNone,
		}, nil
	}

	if maxDepth <= 0 {
		maxDepth = 2
		if d, ok := impactDepthDefaults[seed.NodeType]; ok {
			maxDepth = d
		}
	}

	relTypes := relationshipTypes
	if len(relTypes) == 0 {
		relTypes = defaultImpactRelationshipStrings(seed.NodeType)
	}

	// Per-account environment tiers, the fallback for dependents whose node
	// carries no environment attribute of its own. Fail open: environment is an
	// enrichment, so a lookup failure degrades to "environment unknown"
	// (EnvironmentResolved stays false) rather than killing the traversal.
	accountEnv, err := s.loadAccountEnvs(tenantID)
	if err != nil {
		s.logger.Warn("account environment lookup failed; blast radius proceeds without environment resolution",
			"tenant_id", tenantID, "error", err)
		accountEnv = map[string]string{}
	}

	discoveredIDs, _, nodeMinDepth, err := s.discoverBFS([]string{nodeID}, traverseOptions{
		Direction:         TraverseDirectionUpstream,
		Levels:            maxDepth,
		RelationshipTypes: relTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("impact traversal for %s: %w", nodeID, err)
	}

	truncated := false
	if len(discoveredIDs) > maxImpactNodes {
		discoveredIDs = discoveredIDs[:maxImpactNodes]
		truncated = true
	}

	nodes, err := s.fetchNodesByIDs(discoveredIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch impacted nodes: %w", err)
	}
	// Keep only same-tenant nodes and re-scope the edge fetch to them, so a node
	// ID that somehow resolves cross-tenant cannot leak into the result.
	nodes = filterNodesByTenant(nodes, tenantID)
	discoveredIDs = nodeIDsOf(nodes)

	edges, err := s.fetchEdgesBetweenNodesFiltered(discoveredIDs, relTypes)
	if err != nil {
		return nil, fmt.Errorf("fetch impact edges: %w", err)
	}

	summary := summarizeImpact(nodeID, seed.NodeType, nodes, edges, nodeMinDepth, accountEnv)
	summary.Truncated = truncated
	summary.EnvironmentResolved = len(accountEnv) > 0

	if downRels := downstreamRelationshipStrings(seed.NodeType); len(downRels) > 0 {
		downstream, err := s.traverseDownstreamDependencies(tenantID, nodeID, downRels, maxDepth, accountEnv)
		if err != nil {
			return nil, err
		}
		summary.DownstreamDependencies = downstream
		summary.DownstreamCount = len(downstream)
	}

	// A volume's dependent instance sits on an OUTGOING edge (Storage
	// --HOSTED_ON--> ComputeInstance is how both EBS and GCP-PD model
	// attachment), invisible to the upstream walk. Resolve it with a targeted
	// one-hop fetch into InfrastructureDependents — reported, never counted in
	// the band's DependentCount (an instance is not an app caller), but the
	// destructive-change floor in the recommendation layer refuses to soften
	// while anything at all is attached. Depth is deliberately 1: HOSTED_ON is
	// a non-selective legacy type (instances are HOSTED_ON subnets/VPCs too),
	// so one more hop would drag networking plumbing into the report.
	if seed.NodeType == NodeTypeStorage {
		attached, err := s.attachedInstanceDependents(tenantID, nodeID, accountEnv)
		if err != nil {
			return nil, err
		}
		if len(attached) > 0 {
			summary.InfrastructureDependents = append(summary.InfrastructureDependents, attached...)
			sortImpactedServices(summary.InfrastructureDependents)
			summary.InfrastructureCount = len(summary.InfrastructureDependents)
		}
	}

	// Instance/Node seeds get a hosted-workload rollup from live pod placement:
	// workloads (with pod counts), not raw replicas. A separate list by design —
	// a hosted workload reschedules rather than breaks, so it must not inflate
	// DependentCount (see the HostedWorkloads field comment).
	if seed.NodeType == NodeTypeComputeInstance || seed.NodeType == NodeTypeNode {
		hosted, err := s.hostedWorkloadDependents(seed, nodes, nodeMinDepth, accountEnv)
		if err != nil {
			return nil, err
		}
		summary.HostedWorkloads = hosted
		summary.HostedWorkloadCount = len(hosted)
	}

	// Upgrade low → observed when an active traffic signal demonstrably watches
	// this resource: an edge touching the seed, or — for cluster-scoped seeds
	// only — any flow-asserted edge in its cloud account. This is what lets a
	// single-source tenant reach an honest "no callers" verdict instead of a
	// permanent "low".
	if summary.CoverageConfidence == CoverageLow {
		observed := hasSeedFlowEvidence(nodeID, edges, summary.DownstreamDependencies)
		if !observed && seedAccountIsClusterScoped(seed) {
			observed, err = s.accountHasFlowObservedEdges(tenantID, seed.CloudAccountID)
			if err != nil {
				return nil, err
			}
		}
		if observed {
			summary.CoverageConfidence = CoverageObserved
		}
	}
	return &summary, nil
}

// seedAccountIsClusterScoped reports whether the seed's cloud account maps to
// a single observable scope. k8s-sourced nodes live in per-cluster accounts
// (one cluster == one account row), so account-level flow evidence means the
// seed's own cluster is watched. Cloud-sourced resources live in provider
// accounts that may span several clusters plus uninstrumented callers (VMs,
// external apps) — account-level evidence there could vouch for resources the
// signal never watched, so they get no fallback and rely on seed-adjacent
// evidence alone.
func seedAccountIsClusterScoped(seed *DbNode) bool {
	return seed != nil && seed.Source == "k8s"
}

// hasSeedFlowEvidence reports whether an active traffic signal asserted any
// edge touching the seed — an upstream dependency edge incident to it, or a
// downstream dependency attributed to a flow source. Either way the signal
// demonstrably watches this resource, so finding no callers is evidence of
// absence rather than absence of evidence.
func hasSeedFlowEvidence(seedID string, upstreamEdges []*DbEdge, downstream []ImpactedService) bool {
	for _, e := range upstreamEdges {
		if e == nil || (e.SourceNodeID != seedID && e.DestinationNodeID != seedID) {
			continue
		}
		if edgeHasFlowSource(e) {
			return true
		}
	}
	for _, d := range downstream {
		// Only dependencies one hop away vouch for the seed: their attributed
		// edge touches it. Deeper entries exist since the downstream walk went
		// multi-hop, and a flow-asserted edge between hop 1 and hop 2 (e.g.
		// product-catalog -> postgres) says nothing about whether the seed's
		// own edges are watched.
		if d.HopsAway != 1 {
			continue
		}
		for _, s := range d.Sources {
			if flowObservationSources[s] {
				return true
			}
		}
	}
	return false
}

// edgeHasFlowSource reports whether any asserting source of the edge is an
// active traffic signal, falling back to the winning Source for edges
// predating the contributing_sources column.
func edgeHasFlowSource(e *DbEdge) bool {
	for _, cs := range e.ContributingSources {
		if flowObservationSources[cs.Source] {
			return true
		}
	}
	return len(e.ContributingSources) == 0 && flowObservationSources[e.Source]
}

// accountHasFlowObservedEdges reports whether any active flow signal produced
// an edge in this tenant+account — the scope-level fallback: with the account
// under observation, a resource nothing points at is meaningfully
// unreferenced. Account-scoped on purpose: a monitored cluster in one account
// must not vouch for resources in an unmonitored one.
func (s *Service) accountHasFlowObservedEdges(tenantID, accountID string) (bool, error) {
	if accountID == "" {
		return false, nil
	}
	sources := make([]string, 0, len(flowObservationSources))
	for src := range flowObservationSources {
		sources = append(sources, src)
	}
	sort.Strings(sources)
	// `source` holds only the winning source after dedup (k8s outranks flow
	// sources), so contributing_sources is probed too — as one explicit @>
	// clause per source rather than `@> ANY(...)`, which GIN indexes cannot
	// serve (no ScalarArrayOpExpr support; ANY would decay to scanning the
	// tenant's edges).
	conds := []string{"source = ANY($3::text[])"}
	args := []interface{}{tenantID, accountID, pq.Array(sources)}
	for _, src := range sources {
		conds = append(conds, fmt.Sprintf("contributing_sources @> $%d::jsonb", len(args)+1))
		args = append(args, fmt.Sprintf(`[{"source": %q}]`, src))
	}
	query := fmt.Sprintf(`
		SELECT EXISTS (
			SELECT 1 FROM knowledge_graph_edge
			WHERE tenant_id = $1
			  AND cloud_account_id = $2
			  AND is_active = true
			  AND level = 'Tenant'
			  AND (%s)
		)`, strings.Join(conds, " OR "))
	row, err := s.dbManager.QueryRow(query, args...)
	if err != nil {
		return false, fmt.Errorf("flow-observation probe for account %s: %w", accountID, err)
	}
	var exists bool
	if err := row.Scan(&exists); err != nil {
		return false, fmt.Errorf("scan flow-observation probe for account %s: %w", accountID, err)
	}
	return exists, nil
}

// attachedInstanceDependents resolves the compute instance(s) a volume seed is
// attached to — one hop over the seed's outgoing HOSTED_ON edges — as
// InfrastructureDependents entries. Attribution is set directly (the edges are
// fetched here, not through the BFS attribution layer) and these edges are
// deliberately kept out of coverage grading: attachment is static metadata and
// must not mint "well-observed".
func (s *Service) attachedInstanceDependents(tenantID, seedID string, accountEnv map[string]string) ([]ImpactedService, error) {
	relTypes := []string{string(RelationshipHostedOn)}
	discoveredIDs, _, depths, err := s.discoverBFS([]string{seedID}, traverseOptions{
		Direction:         TraverseDirectionDownstream,
		Levels:            1,
		RelationshipTypes: relTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("attachment traversal for %s: %w", seedID, err)
	}
	nodes, err := s.fetchNodesByIDs(discoveredIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch attached nodes: %w", err)
	}
	nodes = filterNodesByTenant(nodes, tenantID)
	edges, err := s.fetchEdgesBetweenNodesFiltered(nodeIDsOf(nodes), relTypes)
	if err != nil {
		return nil, fmt.Errorf("fetch attachment edges: %w", err)
	}
	attribution := attributeConnectingEdges(edges, depths, TraverseDirectionDownstream)

	var out []ImpactedService
	for _, n := range nodes {
		if n == nil || n.ID == seedID || n.NodeType != NodeTypeComputeInstance {
			continue
		}
		att := attribution[n.ID]
		out = append(out, ImpactedService{
			NodeID:       n.ID,
			Name:         impactNodeName(n),
			ResourceID:   impactNodeAttr(n, "resource_id"),
			NodeType:     n.NodeType,
			Environment:  resolveNodeEnvironment(n, accountEnv),
			HopsAway:     1,
			Relationship: att.relationship,
			Sources:      att.sources,
		})
	}
	return out, nil
}

// hostedWorkloadDependents rolls live pod placement (k8s_pods) up to workloads
// for an instance/node seed: for the seed itself when it is a k8s Node, or for
// every k8s Node discovered by the upstream traversal when the seed is a cloud
// instance. Two batched queries total (one placement GROUP BY, one workload
// entity lookup) regardless of node count. Workload groups whose entity is not
// in the graph (unsupported owner kinds) still surface by identity; standalone
// pods (no owning workload) are not rolled up — they remain visible through
// the regular traversal wherever they exist as graph nodes.
func (s *Service) hostedWorkloadDependents(seed *DbNode, traversed []*DbNode, nodeMinDepth map[string]int, accountEnv map[string]string) ([]ImpactedService, error) {
	if s.podSynth == nil {
		return nil, nil
	}
	// Collect the k8s Node entities to roll up, with their hop distance from
	// the seed (a workload sits one hop past its node).
	type nodeRef struct {
		node *DbNode
		hops int
	}
	var nodeRefs []nodeRef
	if seed.NodeType == NodeTypeNode {
		nodeRefs = append(nodeRefs, nodeRef{node: seed, hops: 0})
	}
	for _, n := range traversed {
		if n != nil && n.ID != seed.ID && n.NodeType == NodeTypeNode {
			nodeRefs = append(nodeRefs, nodeRef{node: n, hops: nodeMinDepth[n.ID]})
		}
	}
	if len(nodeRefs) == 0 {
		return nil, nil
	}

	// k8s Nodes live in per-cluster accounts; group per (tenant, account) so
	// the placement query stays correctly scoped (a cloud-instance seed's own
	// account differs from its cluster's).
	type scopeKey struct{ tenantID, accountID string }
	byScope := map[scopeKey][]nodeRef{}
	for _, ref := range nodeRefs {
		if ref.node.TenantID == "" || ref.node.CloudAccountID == "" {
			continue
		}
		k := scopeKey{ref.node.TenantID, ref.node.CloudAccountID}
		byScope[k] = append(byScope[k], ref)
	}

	var out []ImpactedService
	for scope, refs := range byScope {
		names := make([]string, 0, len(refs))
		hopsByNodeName := map[string]int{}
		for _, ref := range refs {
			name, _ := ref.node.Properties["name"].(string)
			if name == "" {
				continue
			}
			names = append(names, name)
			if prev, ok := hopsByNodeName[name]; !ok || ref.hops < prev {
				hopsByNodeName[name] = ref.hops
			}
		}
		if len(names) == 0 {
			continue
		}
		rollups, err := s.podSynth.WorkloadRollupForNodes(scope.tenantID, scope.accountID, names)
		if err != nil {
			return nil, fmt.Errorf("hosted workload rollup: %w", err)
		}
		if len(rollups) == 0 {
			continue
		}
		entities, err := s.podSynth.WorkloadEntitiesByIdentity(scope.tenantID, scope.accountID, rollups)
		if err != nil {
			return nil, fmt.Errorf("hosted workload entity lookup: %w", err)
		}
		for _, r := range rollups {
			entry := ImpactedService{
				Name:         r.WorkloadName,
				NodeType:     NodeTypeWorkload,
				Namespace:    r.Namespace,
				HopsAway:     hopsByNodeName[r.NodeName] + 1,
				Relationship: RelationshipRunsOn,
				Sources:      []string{"k8s"},
				PodCount:     r.PodCount,
				Environment:  accountEnv[scope.accountID],
			}
			if wl := entities[r.identityKey()]; wl != nil {
				entry.NodeID = wl.ID
				entry.Name = impactNodeName(wl)
				entry.ResourceID = impactNodeAttr(wl, "resource_id")
				entry.Environment = resolveNodeEnvironment(wl, accountEnv)
			}
			out = append(out, entry)
		}
	}
	sortImpactedServices(out)
	return out, nil
}

// traverseDownstreamDependencies walks in the opposite direction — edges whose
// source is the seed — to name what the seed itself depends on. It walks to the
// same maxDepth as the dependents traversal: for change-safety a dependency's
// own dependencies are unaffected by changing the seed, but for cause
// attribution the propagation is real in this direction too — a failing
// transitive dependency (frontend → product-catalog → postgres) breaks the
// seed, and a depth-1 list hid exactly those roots from the incident cause
// lane while the depth-2 dependents walk showed the seed from the root's side.
func (s *Service) traverseDownstreamDependencies(tenantID, nodeID string, relTypes []string, maxDepth int, accountEnv map[string]string) ([]ImpactedService, error) {
	discoveredIDs, _, nodeMinDepth, err := s.discoverBFS([]string{nodeID}, traverseOptions{
		Direction:         TraverseDirectionDownstream,
		Levels:            maxDepth,
		RelationshipTypes: relTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("downstream traversal for %s: %w", nodeID, err)
	}
	if len(discoveredIDs) > maxImpactNodes {
		discoveredIDs = discoveredIDs[:maxImpactNodes]
	}
	nodes, err := s.fetchNodesByIDs(discoveredIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch downstream nodes: %w", err)
	}
	nodes = filterNodesByTenant(nodes, tenantID)
	edges, err := s.fetchEdgesBetweenNodesFiltered(nodeIDsOf(nodes), relTypes)
	if err != nil {
		return nil, fmt.Errorf("fetch downstream edges: %w", err)
	}
	return summarizeDownstream(nodeID, nodes, edges, nodeMinDepth, accountEnv), nil
}

// summarizeImpact is the pure (DB-free) aggregation behind GetImpactedServices:
// given the traversed nodes/edges it rolls up the application-level dependents,
// production exposure, and a coverage-confidence signal. Kept separate so the
// logic is unit-testable without a live graph. accountEnv is required (pass an
// empty map for no fallback) so no caller can silently opt out of environment
// resolution — the always-zero prod count this replaces came from exactly that.
func summarizeImpact(seedID string, seedType NodeType, nodes []*DbNode, edges []*DbEdge, nodeMinDepth map[string]int, accountEnv map[string]string) ImpactSummary {
	summary := ImpactSummary{
		SeedNodeID:       seedID,
		SeedNodeType:     seedType,
		DependentsByType: map[NodeType]int{},
		Dependents:       []ImpactedService{},
	}

	attribution := attributeConnectingEdges(edges, nodeMinDepth, TraverseDirectionUpstream)
	for _, n := range nodes {
		if n == nil || n.ID == seedID || !canBeImpacted(n.NodeType) {
			continue
		}
		summary.DependentsByType[n.NodeType]++
		if !appDependentTypes[n.NodeType] {
			att := attribution[n.ID]
			summary.InfrastructureDependents = append(summary.InfrastructureDependents, ImpactedService{
				NodeID:       n.ID,
				Name:         impactNodeName(n),
				ResourceID:   impactNodeAttr(n, "resource_id"),
				NodeType:     n.NodeType,
				Namespace:    impactNodeAttr(n, "namespace"),
				Environment:  resolveNodeEnvironment(n, accountEnv),
				HopsAway:     nodeMinDepth[n.ID],
				Relationship: att.relationship,
				Sources:      att.sources,
			})
			continue
		}
		env := resolveNodeEnvironment(n, accountEnv)
		att := attribution[n.ID]
		summary.Dependents = append(summary.Dependents, ImpactedService{
			NodeID:       n.ID,
			Name:         impactNodeName(n),
			ResourceID:   impactNodeAttr(n, "resource_id"),
			NodeType:     n.NodeType,
			Namespace:    impactNodeAttr(n, "namespace"),
			Environment:  env,
			HopsAway:     nodeMinDepth[n.ID],
			Relationship: att.relationship,
			Sources:      att.sources,
		})
		summary.DependentCount++
		if isProdEnv(env) {
			summary.ProductionDependents++
		}
	}

	sortImpactedServices(summary.Dependents)
	sortImpactedServices(summary.InfrastructureDependents)
	summary.InfrastructureCount = len(summary.InfrastructureDependents)
	summary.CoverageConfidence = coverageFromEdges(edges)
	return summary
}

// summarizeDownstream is the pure aggregation for the downstream pass: the
// one-hop nodes the seed depends on, with edge attribution. No coverage or
// production rollup — downstream is context only. accountEnv follows the same
// required-argument contract as summarizeImpact.
func summarizeDownstream(seedID string, nodes []*DbNode, edges []*DbEdge, nodeMinDepth map[string]int, accountEnv map[string]string) []ImpactedService {
	attribution := attributeConnectingEdges(edges, nodeMinDepth, TraverseDirectionDownstream)
	deps := []ImpactedService{}
	for _, n := range nodes {
		if n == nil || n.ID == seedID || !downstreamDependencyTypes[n.NodeType] {
			continue
		}
		att := attribution[n.ID]
		deps = append(deps, ImpactedService{
			NodeID:       n.ID,
			Name:         impactNodeName(n),
			ResourceID:   impactNodeAttr(n, "resource_id"),
			NodeType:     n.NodeType,
			Namespace:    impactNodeAttr(n, "namespace"),
			Environment:  resolveNodeEnvironment(n, accountEnv),
			HopsAway:     nodeMinDepth[n.ID],
			Relationship: att.relationship,
			Sources:      att.sources,
		})
	}
	sortImpactedServices(deps)
	return deps
}

// sortImpactedServices orders internal dependents before unresolved external
// callers (ExternalService nodes are bare IPs nobody can act on), then
// closest-first, then by name — so bounded consumers keep the most relevant
// slice and the UI leads with named services.
func sortImpactedServices(deps []ImpactedService) {
	sort.Slice(deps, func(i, j int) bool {
		iExternal := deps[i].NodeType == NodeTypeExternalService
		jExternal := deps[j].NodeType == NodeTypeExternalService
		if iExternal != jExternal {
			return !iExternal
		}
		if deps[i].HopsAway != deps[j].HopsAway {
			return deps[i].HopsAway < deps[j].HopsAway
		}
		return deps[i].Name < deps[j].Name
	})
}

// connectingEdgeAttribution is the per-node provenance rollup derived from the
// edge(s) linking a discovered node one hop toward the seed.
type connectingEdgeAttribution struct {
	relationship RelationshipType
	sources      []string
}

// attributeConnectingEdges maps each discovered node to the relationship and
// discovery sources of its connecting edge(s) — for upstream traversal the
// node is the edge's source stepping to a destination one BFS layer closer to
// the seed; downstream is the mirror. Sibling edges (same-depth endpoints) are
// ignored. When several edges connect the same node, the best-corroborated one
// names the relationship (ties broken lexically so attribution is
// deterministic) and sources are unioned across all of them.
func attributeConnectingEdges(edges []*DbEdge, nodeMinDepth map[string]int, direction TraverseDirection) map[string]connectingEdgeAttribution {
	best := map[string]*DbEdge{}
	sourceSets := map[string]map[string]struct{}{}
	for _, e := range edges {
		if e == nil {
			continue
		}
		nodeID, towardSeedID := e.SourceNodeID, e.DestinationNodeID
		if direction == TraverseDirectionDownstream {
			nodeID, towardSeedID = e.DestinationNodeID, e.SourceNodeID
		}
		nodeDepth, ok := nodeMinDepth[nodeID]
		if !ok {
			continue
		}
		if towardDepth, ok := nodeMinDepth[towardSeedID]; !ok || towardDepth != nodeDepth-1 {
			continue
		}
		set := sourceSets[nodeID]
		if set == nil {
			set = map[string]struct{}{}
			sourceSets[nodeID] = set
		}
		for _, cs := range e.ContributingSources {
			if cs.Source != "" {
				set[cs.Source] = struct{}{}
			}
		}
		// Edges predating the contributing_sources column fall back to the
		// winning source so provenance is never silently empty.
		if len(e.ContributingSources) == 0 && e.Source != "" {
			set[e.Source] = struct{}{}
		}
		if cur, ok := best[nodeID]; !ok || betterConnectingEdge(e, cur) {
			best[nodeID] = e
		}
	}
	out := make(map[string]connectingEdgeAttribution, len(best))
	for id, e := range best {
		sources := make([]string, 0, len(sourceSets[id]))
		for s := range sourceSets[id] {
			sources = append(sources, s)
		}
		sort.Strings(sources)
		out[id] = connectingEdgeAttribution{relationship: e.RelationshipType, sources: sources}
	}
	return out
}

// betterConnectingEdge prefers the better-corroborated edge; ties break on the
// lexically smaller relationship type.
func betterConnectingEdge(a, b *DbEdge) bool {
	if len(a.ContributingSources) != len(b.ContributingSources) {
		return len(a.ContributingSources) > len(b.ContributingSources)
	}
	return a.RelationshipType < b.RelationshipType
}

// coverageFromEdges grades trust in the dependency picture: high when at least
// one dependency edge is corroborated by multiple discovery sources, otherwise
// low. (Seed-absent → CoverageNone is decided before traversal, by the caller.)
func coverageFromEdges(edges []*DbEdge) CoverageConfidence {
	for _, e := range edges {
		if e != nil && len(e.ContributingSources) >= 2 {
			return CoverageHigh
		}
	}
	return CoverageLow
}

// impactNodeName prefers the indexed name, falling back to the unique key.
func impactNodeName(n *DbNode) string {
	if v := impactNodeAttr(n, "name"); v != "" {
		return v
	}
	return n.UniqueKey
}

// impactNodeAttr reads a string attribute, preferring query_attributes (indexed)
// and falling back to the full properties map.
func impactNodeAttr(n *DbNode, key string) string {
	if n.QueryAttributes != nil {
		if v, ok := n.QueryAttributes[key].(string); ok && v != "" {
			return v
		}
	}
	if n.Properties != nil {
		if v, ok := n.Properties[key].(string); ok {
			return v
		}
	}
	return ""
}

func isProdEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production", "prd":
		return true
	default:
		return false
	}
}

// resolveNodeEnvironment returns a node's environment: its own environment
// attribute when set (the specific claim — a workload label — wins), otherwise
// the environment tier of the cloud account it belongs to. ExternalService is
// excluded from the account fallback: those nodes are unresolved callers whose
// CloudAccountID records the account that *observed* them, not where they run,
// so stamping them with the observer's tier would let a dev batch job's IP
// count as a production dependent of a prod database.
func resolveNodeEnvironment(n *DbNode, accountEnv map[string]string) string {
	if env := impactNodeAttr(n, "environment"); env != "" {
		return env
	}
	if n.NodeType == NodeTypeExternalService {
		return ""
	}
	return accountEnv[n.CloudAccountID]
}

// loadAccountEnvs returns the tenant's per-account environment tiers
// (cloud_accounts.account_env — 'prod'/'non_prod', NOT NULL with a 'non_prod'
// default) keyed by account id. Deliberately uncached: the tenant's account
// list is tiny, the finops recompute already memoizes per resource, and the
// triage panel issues one call per render. (triage/scoring.go keeps its own
// cached single-account variant; the query is not worth sharing across that
// package boundary.)
func (s *Service) loadAccountEnvs(tenantID string) (map[string]string, error) {
	rows, err := s.dbManager.Query(`SELECT id, account_env FROM cloud_accounts WHERE tenant = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query account environments: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close account environment rows", "error", closeErr)
		}
	}()
	envs := map[string]string{}
	for rows.Next() {
		var id, env string
		if err := rows.Scan(&id, &env); err != nil {
			return nil, fmt.Errorf("scan account environment: %w", err)
		}
		envs[id] = env
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account environments: %w", err)
	}
	return envs, nil
}

// filterNodesByTenant drops any node not belonging to tenantID — a defensive
// re-scoping on top of the node-ID lookup the traversal helpers use.
func filterNodesByTenant(nodes []*DbNode, tenantID string) []*DbNode {
	filtered := make([]*DbNode, 0, len(nodes))
	for _, n := range nodes {
		if n != nil && n.TenantID == tenantID {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

// nodeIDsOf returns the IDs of the given nodes, preserving order.
func nodeIDsOf(nodes []*DbNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	return ids
}
