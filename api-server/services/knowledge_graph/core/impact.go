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
	NodeID      string   `json:"node_id"`
	Name        string   `json:"name"`
	NodeType    NodeType `json:"node_type"`
	Namespace   string   `json:"namespace,omitempty"`
	Environment string   `json:"environment,omitempty"`
	HopsAway    int      `json:"hops_away"`
	// Relationship is the edge type linking this node one hop toward the seed
	// (its own edge when direct, its first walked edge when multi-hop); Sources
	// is the union of discovery sources asserting any such edge — the provenance
	// behind the dependency claim.
	Relationship RelationshipType `json:"relationship,omitempty"`
	Sources      []string         `json:"sources,omitempty"`
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
	// DownstreamDependencies is the reverse direction: what the seed itself
	// calls, publishes to, or subscribes to (one hop). Operator context —
	// deliberately excluded from DependentCount and the safety band, which
	// grade risk to callers only.
	DownstreamDependencies []ImpactedService `json:"downstream_dependencies,omitempty"`
	DownstreamCount        int               `json:"downstream_count,omitempty"`
}

// impactRelationshipDefaults maps a resource node type to the relationship types
// whose source is the dependent. Traversal is always upstream (destination →
// source), so for a Database (Service CALLS Database) the upstream neighbour is
// the calling Service; for a ComputeInstance (Node/Pod RUNS_ON Instance) it is
// the hosted workloads, etc. Callers may override.
var impactRelationshipDefaults = map[NodeType][]RelationshipType{
	NodeTypeDatabase:        {RelationshipCalls},
	NodeTypeCache:           {RelationshipCalls},
	NodeTypeStorage:         {RelationshipCalls, RelationshipProvidesStorage},
	NodeTypeMessageQueue:    {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	NodeTypeQueue:           {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	NodeTypeTopic:           {RelationshipCalls, RelationshipPublishesTo, RelationshipSubscribesTo},
	NodeTypeComputeInstance: {RelationshipRunsOn, RelationshipManages, RelationshipOwns},
	NodeTypeNode:            {RelationshipRunsOn, RelationshipManages, RelationshipOwns},
	NodeTypePV:              {RelationshipProvidesStorage, RelationshipIsBoundTo, RelationshipMounts},
	NodeTypePVC:             {RelationshipMounts, RelationshipIsBoundTo},
	NodeTypeK8sService:      {RelationshipExposes, RelationshipRoutesToService},
	NodeTypeLoadBalancer:    {RelationshipRoutesToBackend, RelationshipRoutesToService},
	NodeTypeWorkload:        {RelationshipCalls},
	NodeTypeService:         {RelationshipCalls},
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
	if maxDepth <= 0 {
		maxDepth = 2
	}
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

	relTypes := relationshipTypes
	if len(relTypes) == 0 {
		relTypes = defaultImpactRelationshipStrings(seed.NodeType)
	}

	discoveredIDs, _, nodeMinDepth, err := s.discoverDirectional(
		[]string{nodeID}, TraverseDirectionUpstream, maxDepth, relTypes, nil, nil)
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

	summary := summarizeImpact(nodeID, seed.NodeType, nodes, edges, nodeMinDepth)
	summary.Truncated = truncated

	if downRels := downstreamRelationshipStrings(seed.NodeType); len(downRels) > 0 {
		downstream, err := s.traverseDownstreamDependencies(tenantID, nodeID, downRels, maxDepth)
		if err != nil {
			return nil, err
		}
		summary.DownstreamDependencies = downstream
		summary.DownstreamCount = len(downstream)
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

// traverseDownstreamDependencies walks in the opposite direction — edges whose
// source is the seed — to name what the seed itself depends on. It walks to the
// same maxDepth as the dependents traversal: for change-safety a dependency's
// own dependencies are unaffected by changing the seed, but for cause
// attribution the propagation is real in this direction too — a failing
// transitive dependency (frontend → product-catalog → postgres) breaks the
// seed, and a depth-1 list hid exactly those roots from the incident cause
// lane while the depth-2 dependents walk showed the seed from the root's side.
func (s *Service) traverseDownstreamDependencies(tenantID, nodeID string, relTypes []string, maxDepth int) ([]ImpactedService, error) {
	discoveredIDs, _, nodeMinDepth, err := s.discoverDirectional(
		[]string{nodeID}, TraverseDirectionDownstream, maxDepth, relTypes, nil, nil)
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
	return summarizeDownstream(nodeID, nodes, edges, nodeMinDepth), nil
}

// summarizeImpact is the pure (DB-free) aggregation behind GetImpactedServices:
// given the traversed nodes/edges it rolls up the application-level dependents,
// production exposure, and a coverage-confidence signal. Kept separate so the
// logic is unit-testable without a live graph.
func summarizeImpact(seedID string, seedType NodeType, nodes []*DbNode, edges []*DbEdge, nodeMinDepth map[string]int) ImpactSummary {
	summary := ImpactSummary{
		SeedNodeID:       seedID,
		SeedNodeType:     seedType,
		DependentsByType: map[NodeType]int{},
		Dependents:       []ImpactedService{},
	}

	attribution := attributeConnectingEdges(edges, nodeMinDepth, TraverseDirectionUpstream)
	for _, n := range nodes {
		if n == nil || n.ID == seedID {
			continue
		}
		summary.DependentsByType[n.NodeType]++
		if !appDependentTypes[n.NodeType] {
			continue
		}
		env := impactNodeAttr(n, "environment")
		att := attribution[n.ID]
		summary.Dependents = append(summary.Dependents, ImpactedService{
			NodeID:       n.ID,
			Name:         impactNodeName(n),
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
	summary.CoverageConfidence = coverageFromEdges(edges)
	return summary
}

// summarizeDownstream is the pure aggregation for the downstream pass: the
// one-hop nodes the seed depends on, with edge attribution. No coverage or
// production rollup — downstream is context only.
func summarizeDownstream(seedID string, nodes []*DbNode, edges []*DbEdge, nodeMinDepth map[string]int) []ImpactedService {
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
			NodeType:     n.NodeType,
			Namespace:    impactNodeAttr(n, "namespace"),
			Environment:  impactNodeAttr(n, "environment"),
			HopsAway:     nodeMinDepth[n.ID],
			Relationship: att.relationship,
			Sources:      att.sources,
		})
	}
	sortImpactedServices(deps)
	return deps
}

// sortImpactedServices orders closest-first, then by name, so bounded
// consumers keep the most relevant slice.
func sortImpactedServices(deps []ImpactedService) {
	sort.Slice(deps, func(i, j int) bool {
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
