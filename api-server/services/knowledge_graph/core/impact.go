package core

import (
	"fmt"
	"sort"
	"strings"
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
	// CoverageLow — the resource is present, but dependents (if any) were observed
	// via only a single discovery source, or none were observed at all.
	CoverageLow CoverageConfidence = "low"
	// CoverageHigh — at least one dependency edge is corroborated by multiple
	// discovery sources (e.g. traces + Datadog).
	CoverageHigh CoverageConfidence = "high"
)

// ImpactedService is one dependent of a resource: an application-level node that
// relies on it and could be affected if the resource is rightsized or removed.
type ImpactedService struct {
	NodeID      string   `json:"node_id"`
	Name        string   `json:"name"`
	NodeType    NodeType `json:"node_type"`
	Namespace   string   `json:"namespace,omitempty"`
	Environment string   `json:"environment,omitempty"`
	HopsAway    int      `json:"hops_away"`
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

	const maxImpactNodes = 500
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
	return &summary, nil
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

	for _, n := range nodes {
		if n == nil || n.ID == seedID {
			continue
		}
		summary.DependentsByType[n.NodeType]++
		if !appDependentTypes[n.NodeType] {
			continue
		}
		env := impactNodeAttr(n, "environment")
		summary.Dependents = append(summary.Dependents, ImpactedService{
			NodeID:      n.ID,
			Name:        impactNodeName(n),
			NodeType:    n.NodeType,
			Namespace:   impactNodeAttr(n, "namespace"),
			Environment: env,
			HopsAway:    nodeMinDepth[n.ID],
		})
		summary.DependentCount++
		if isProdEnv(env) {
			summary.ProductionDependents++
		}
	}

	sort.Slice(summary.Dependents, func(i, j int) bool {
		if summary.Dependents[i].HopsAway != summary.Dependents[j].HopsAway {
			return summary.Dependents[i].HopsAway < summary.Dependents[j].HopsAway
		}
		return summary.Dependents[i].Name < summary.Dependents[j].Name
	})

	summary.CoverageConfidence = coverageFromEdges(edges)
	return summary
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
