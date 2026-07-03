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

// recommendationImpact bundles the safety band plus a compact impact summary,
// shaped for embedding in a recommendation's finops_score_breakdown JSONB.
type recommendationImpact struct {
	Band    SafetyBand
	Reason  string
	Summary map[string]any
}

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
// driver), and proximity (hops from the changed resource) — and deliberately
// drops the graph-internal node_id, an opaque UUID of no use downstream.
type dependentRef struct {
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name"`
	NodeType    string `json:"node_type"`
	Environment string `json:"environment,omitempty"`
	HopsAway    int    `json:"hops_away"`
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
			Namespace:   d.Namespace,
			Name:        d.Name,
			NodeType:    string(d.NodeType),
			Environment: d.Environment,
			HopsAway:    d.HopsAway,
		}
	}
	return out
}

// resolveK8sRecommendationImpact resolves a k8s recommendation to its workload
// node, computes the blast radius, and derives the safety band. ok=false means
// the recommendation could not be resolved (caller should leave it unannotated).
func resolveK8sRecommendationImpact(kg *core.Service, tenantID, accountID string, ref k8sRecRef) (recommendationImpact, bool) {
	nodeID, ok := resolveK8sWorkloadNodeID(kg, tenantID, accountID, ref)
	if !ok {
		return recommendationImpact{}, false
	}
	impact, err := kg.GetImpactedServices(tenantID, nodeID, nil, 2)
	if err != nil {
		return recommendationImpact{}, false
	}
	band, reason := DeriveSafetyBand(impact)
	return recommendationImpact{
		Band:   band,
		Reason: reason,
		Summary: map[string]any{
			"dependent_count":       impact.DependentCount,
			"production_dependents": impact.ProductionDependents,
			"coverage_confidence":   string(impact.CoverageConfidence),
			"truncated":             impact.Truncated,
			"safety_reason":         reason,
			"dependents":            compactDependents(impact.Dependents),
		},
	}, true
}

// annotateBreakdownWithImpact resolves a k8s recommendation's blast radius and
// stamps safety_band + impact_summary into the score breakdown map. namespace and
// workload are the recommendation's identity as the caller derived it from the
// cloud_resourses join the recommendations view uses — NOT the raw recommendation
// JSONB, whose shape varies per rule type and frequently omits the namespace. It
// is a no-op when identity is incomplete (non-k8s), or the workload is ambiguous /
// absent from the graph. Results are memoized per workload via cache.
func annotateBreakdownWithImpact(kg *core.Service, tenantID, accountID, namespace, workload string, breakdown map[string]any, cache map[string]*recommendationImpact) {
	if kg == nil || breakdown == nil {
		return
	}
	namespace = strings.TrimSpace(namespace)
	workload = strings.TrimSpace(workload)
	if namespace == "" || workload == "" {
		return
	}
	key := tenantID + "|" + accountID + "|" + namespace + "|" + workload
	imp, seen := cache[key]
	if !seen {
		// Cache the negative too (nil): a workload that can't be resolved once
		// won't resolve for the next recommendation either, so don't re-run the
		// search + traversal for every rec on the same unresolved workload.
		if resolved, ok := resolveK8sRecommendationImpact(kg, tenantID, accountID, k8sRecRef{Namespace: namespace, Workload: workload}); ok {
			imp = &resolved
		}
		cache[key] = imp
	}
	if imp == nil {
		return
	}
	breakdown["safety_band"] = string(imp.Band)
	breakdown["impact_summary"] = imp.Summary
}
