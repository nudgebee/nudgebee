package recommendation

import (
	"strings"

	"nudgebee/services/knowledge_graph/core"
)

// k8sRecRef is the minimal identity needed to locate a recommendation's workload
// in the knowledge graph.
type k8sRecRef struct {
	Namespace string
	Workload  string
}

// The impact cache memoizes *core.ImpactSummary per resolved resource — NOT a
// derived band: the band depends on each recommendation's change class, and two
// recommendations on the same resource (a rightsizing increase and a cleanup
// delete) must grade independently off the one shared traversal.

// resolveK8sWorkloadNodeID finds the knowledge-graph Workload node for a k8s
// recommendation by (account, namespace, name). Scoping to the recommendation's
// cloud account disambiguates the same namespace/name living in two k8s accounts
// (e.g. dev and prod clusters); the recommendation belongs to exactly one. Any
// remaining ambiguity is treated as unresolved rather than guessed.
func resolveK8sWorkloadNodeID(kg *core.Service, tenantID, accountID string, ref k8sRecRef) (string, bool) {
	params := core.SearchNodesParams{
		Name:      ref.Workload,
		Namespace: ref.Namespace,
		NodeTypes: []core.NodeType{core.NodeTypeWorkload},
		Limit:     2,
	}
	if accountID != "" {
		params.AccountIDs = []string{accountID}
	}
	res, err := kg.SearchNodes(tenantID, params)
	if err != nil || res == nil || len(res.Nodes) != 1 {
		return "", false
	}
	return res.Nodes[0].ID, true
}

// blastRadiusCloudNodeTypes are the cloud resource node types a recommendation
// can resolve to for impact scoring. Databases/caches/queues carry the services
// observed calling them; a compute instance carries its callers plus a hosted-
// workload rollup; a volume resolves its mounting workloads (k8s PV chain,
// upstream) and its attached instance (targeted HOSTED_ON hop); a load balancer
// reports what it fronts through the downstream context pass — all handled
// per-type inside core.GetImpactedServices.
var blastRadiusCloudNodeTypes = []core.NodeType{
	core.NodeTypeComputeInstance,
	core.NodeTypeDatabase,
	core.NodeTypeCache,
	core.NodeTypeMessageQueue,
	core.NodeTypeQueue,
	core.NodeTypeTopic,
	core.NodeTypeStorage,
	core.NodeTypeLoadBalancer,
}

// resolveCloudResourceNodeID finds the knowledge-graph node for a cloud recommendation
// by its resource_id — the cloud_resourses row id the recommendation points at, which
// the graph persists on every node as the nb_resource_id property. Being an exact
// identity match (unlike the k8s path's name lookup) it is robust to duplicate resource
// names, e.g. an autoscaling group whose instances share a Name tag. The node-type
// filter both scopes to resources with a meaningful blast radius and guards against a
// stray non-cloud node sharing the id. Ambiguity (more than one match) is unresolved.
func resolveCloudResourceNodeID(kg *core.Service, tenantID, accountID, resourceID string) (string, bool) {
	params := core.SearchNodesParams{
		ResourceID: resourceID,
		NodeTypes:  blastRadiusCloudNodeTypes,
		Limit:      2,
	}
	if accountID != "" {
		params.AccountIDs = []string{accountID}
	}
	res, err := kg.SearchNodes(tenantID, params)
	if err != nil || res == nil || len(res.Nodes) != 1 {
		return "", false
	}
	return res.Nodes[0].ID, true
}

// maxStoredDependents caps how many dependent identities we persist in the
// impact_summary. Dependents arrive sorted closest-first (fewest hops, then
// name), so the stored prefix is the most relevant slice of a large blast
// radius; dependent_count remains the authoritative total. Bounding the list
// keeps finops_score_breakdown small even for a high-fan-out resource (e.g. a
// shared database with hundreds of callers) that the cron rewrites every 6h.
const maxStoredDependents = 50

// dependentRef is the compact, persisted identity of one blast-radius dependent.
// It carries what the safety UI and @finops agent need to name a dependent and
// judge its risk — identity (namespace/name for k8s; name alone for cloud, where
// namespace is empty and omitted), kind, environment (the production-risk
// driver), proximity (hops from the changed resource), and the connecting
// edge's relationship + discovery sources (how the graph knows) — and
// deliberately drops the graph-internal node_id, an opaque UUID of no use
// downstream.
type dependentRef struct {
	Namespace    string   `json:"namespace,omitempty"`
	Name         string   `json:"name"`
	NodeType     string   `json:"node_type"`
	Environment  string   `json:"environment,omitempty"`
	HopsAway     int      `json:"hops_away"`
	Relationship string   `json:"relationship,omitempty"`
	Sources      []string `json:"sources,omitempty"`
	// PodCount carries the hosted-workload rollup annotation ("Deployment ·
	// 12 pods here"); zero everywhere else and omitted from the JSON.
	PodCount int `json:"pod_count,omitempty"`
}

// compactDependents projects a knowledge-graph blast radius into the bounded list
// of dependent identities stored on the recommendation. Order is preserved (the
// summary is already sorted closest-first) and the result is capped at
// maxStoredDependents. It always returns a non-nil slice so the persisted JSON is
// [] rather than null when there are no dependents. Provider-agnostic: a cloud
// recommendation's dependents flow through unchanged, with namespace omitted.
func compactDependents(deps []core.ImpactedService) []dependentRef {
	n := len(deps)
	if n > maxStoredDependents {
		n = maxStoredDependents
	}
	out := make([]dependentRef, n)
	for i, d := range deps[:n] {
		out[i] = dependentRef{
			Namespace:    d.Namespace,
			Name:         d.Name,
			NodeType:     string(d.NodeType),
			Environment:  d.Environment,
			HopsAway:     d.HopsAway,
			Relationship: string(d.Relationship),
			Sources:      d.Sources,
			PodCount:     d.PodCount,
		}
	}
	return out
}

// buildImpactSummary shapes a knowledge-graph blast radius for embedding in a
// recommendation's finops_score_breakdown JSONB. The shape is provider-agnostic:
// the k8s and cloud resolution paths differ only in how they locate the seed
// node, then share this. reason is band-derived and therefore per-recommendation
// even when the impact itself is shared via the cache.
func buildImpactSummary(impact *core.ImpactSummary, reason string) map[string]any {
	summary := map[string]any{
		"dependent_count":       impact.DependentCount,
		"production_dependents": impact.ProductionDependents,
		// Regime marker: summaries persisted before environment resolution
		// existed lack this key, which is how the UI tells "verified zero
		// production dependents" apart from "environment never resolved" —
		// non-Open recommendations are excluded from the recompute cron, so
		// pre-fix summaries survive indefinitely on resolved recs.
		"environment_resolved":    impact.EnvironmentResolved,
		"coverage_confidence":     string(impact.CoverageConfidence),
		"truncated":               impact.Truncated,
		"safety_reason":           reason,
		"dependents":              compactDependents(impact.Dependents),
		"downstream_count":        impact.DownstreamCount,
		"downstream_dependencies": compactDependents(impact.DownstreamDependencies),
	}
	// The non-caller neighbourhoods are persisted only when present, so the
	// dominant k8s-workload summaries don't grow: attached infrastructure (a
	// volume's instance) and hosted workloads (a node's rollup) explain the
	// destructive-change floor to the UI and the @finops agent.
	if impact.InfrastructureCount > 0 {
		summary["infrastructure_count"] = impact.InfrastructureCount
		summary["infrastructure_dependents"] = compactDependents(impact.InfrastructureDependents)
	}
	if impact.HostedWorkloadCount > 0 {
		summary["hosted_workload_count"] = impact.HostedWorkloadCount
		summary["hosted_workloads"] = compactDependents(impact.HostedWorkloads)
	}
	return summary
}

// resolveK8sRecommendationImpact resolves a k8s recommendation to its workload
// node and computes the blast radius. ok=false means the recommendation could
// not be resolved (caller should leave it unannotated).
func resolveK8sRecommendationImpact(kg *core.Service, tenantID, accountID string, ref k8sRecRef) (*core.ImpactSummary, bool) {
	nodeID, ok := resolveK8sWorkloadNodeID(kg, tenantID, accountID, ref)
	if !ok {
		return nil, false
	}
	impact, err := kg.GetImpactedServices(tenantID, nodeID, nil, 0)
	// Fail closed: guard the nil impact the callers would deref, even though
	// GetImpactedServices only ever returns it paired with an error.
	if err != nil || impact == nil {
		return nil, false
	}
	return impact, true
}

// resolveCloudRecommendationImpact resolves a cloud recommendation to its resource
// node by resource_id and computes the blast radius. Restricted to the cloud
// resource types whose graph edges expose real dependents (see
// blastRadiusCloudNodeTypes); any other cloud resource resolves to no node and
// is left unannotated. ok=false means the recommendation could not be resolved.
func resolveCloudRecommendationImpact(kg *core.Service, tenantID, accountID, resourceID string) (*core.ImpactSummary, bool) {
	nodeID, ok := resolveCloudResourceNodeID(kg, tenantID, accountID, resourceID)
	if !ok {
		return nil, false
	}
	impact, err := kg.GetImpactedServices(tenantID, nodeID, nil, 0)
	// Fail closed: guard the nil impact the callers would deref, even though
	// GetImpactedServices only ever returns it paired with an error.
	if err != nil || impact == nil {
		return nil, false
	}
	return impact, true
}

// annotateBreakdownWithImpact resolves a recommendation's blast radius and stamps
// safety_band + change_class + impact_summary into the score breakdown map.
// Identity comes from the cloud_resourses join the recommendations view uses — NOT
// the raw recommendation JSONB, whose shape varies per rule type and frequently
// omits the namespace. A recommendation carrying a k8s namespace is resolved by
// (namespace, workload) to its Workload node; otherwise a cloud resource
// recommendation is resolved by resource_id to its cloud node. Both paths fail
// safe: a misrouted recommendation resolves to no node (wrong node type / missing
// identity) and is left unannotated, never stamped with another resource's blast
// radius. It is a no-op when neither identity is present, or the resource is
// ambiguous / absent from the graph / not a type we compute impact for.
//
// The graph traversal is memoized per resolved identity via cache; the band is
// derived per call because it also depends on this recommendation's change class.
func annotateBreakdownWithImpact(kg *core.Service, tenantID, accountID, namespace, workload, resourceID string, class ChangeClass, breakdown map[string]any, cache map[string]*core.ImpactSummary) {
	if kg == nil || breakdown == nil {
		return
	}
	namespace = strings.TrimSpace(namespace)
	workload = strings.TrimSpace(workload)
	resourceID = strings.TrimSpace(resourceID)

	var (
		key     string
		resolve func() (*core.ImpactSummary, bool)
	)
	switch {
	case namespace != "" && workload != "":
		key = tenantID + "|" + accountID + "|k8s|" + namespace + "|" + workload
		resolve = func() (*core.ImpactSummary, bool) {
			return resolveK8sRecommendationImpact(kg, tenantID, accountID, k8sRecRef{Namespace: namespace, Workload: workload})
		}
	case resourceID != "":
		key = tenantID + "|" + accountID + "|cloud|" + resourceID
		resolve = func() (*core.ImpactSummary, bool) {
			return resolveCloudRecommendationImpact(kg, tenantID, accountID, resourceID)
		}
	default:
		return
	}

	imp, seen := cache[key]
	if !seen {
		// Cache the negative too (nil): a resource that can't be resolved once won't
		// resolve for the next recommendation either, so don't re-run the search +
		// traversal for every recommendation on the same unresolved resource.
		imp, _ = resolve()
		cache[key] = imp
	}
	if imp == nil {
		return
	}
	band, reason := DeriveSafetyBand(imp, class)
	breakdown["safety_band"] = string(band)
	if class != ChangeClassUnknown {
		breakdown["change_class"] = string(class)
	}
	breakdown["impact_summary"] = buildImpactSummary(imp, reason)
}
