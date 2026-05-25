package core

import (
	"log/slog"
)

// edge-property keys stamped onto a CALLS edge whose destination has been
// rewritten away from an ExternalService. Read by the LLM agent prompts and
// surfaced on the frontend for debugging.
const (
	CollapseEdgePropOriginalHostname    = "original_hostname"
	CollapseEdgePropOriginalESUniqueKey = "original_es_unique_key"
)

type collapseRedirectEntry struct {
	targetID         string
	originalHostname string
	originalUniqKey  string
}

type collapseStats struct {
	rewrittenCalls int
	droppedBridges int
	droppedOther   int
}

// CollapseEnrichedExternalServices removes the ExternalService → CloudResource
// hop from the graph for every ExternalService that was successfully matched
// by cloud_enrichment. It is a pure function over (nodes, edges).
//
// Algorithm:
//  1. Walk edges once. For each edge whose RelationshipType is RoutesThrough
//     or ResolvesTo and whose source is an ExternalService, record
//     redirect[ES.ID] = E.Destination. The first bridge edge wins on conflict.
//  2. Walk edges again, producing collapsedEdges:
//     - bridge edge sourced from a redirected ES → drop
//     - CALLS edge whose destination is a redirected ES → rewrite destination
//     to the redirect target, stamp original_hostname for provenance
//     - any other edge sourced from or pointing at a redirected ES → drop with
//     a warning (no flow source emits this today; defensive)
//     - everything else → keep as-is
//  3. Drop redirected ES nodes from the node slice.
//
// The caller is responsible for running DeduplicateEdgesWithPriority afterwards
// to resolve any collision between a rewritten edge and a directly-observed
// CALLS edge produced by another flow source.
func CollapseEnrichedExternalServices(
	nodes []*DbNode,
	edges []*DbEdge,
	logger *slog.Logger,
) (collapsedNodes []*DbNode, collapsedEdges []*DbEdge, redirectCount int) {
	if logger == nil {
		logger = slog.Default()
	}

	redirect := buildCollapseRedirectMap(nodes, edges, logger)
	if len(redirect) == 0 {
		logger.Debug("collapse_external_services: no enriched ES nodes to collapse")
		return nodes, edges, 0
	}

	collapsedEdges, stats := rewriteEdgesForCollapse(edges, redirect, logger)
	collapsedNodes = filterCollapsedNodes(nodes, redirect)

	logger.Info("collapsed enriched external services",
		"redirected_es_count", len(redirect),
		"rewritten_calls_edges", stats.rewrittenCalls,
		"dropped_bridge_edges", stats.droppedBridges,
		"dropped_unexpected_edges", stats.droppedOther,
		"input_nodes", len(nodes),
		"output_nodes", len(collapsedNodes),
		"input_edges", len(edges),
		"output_edges", len(collapsedEdges))

	return collapsedNodes, collapsedEdges, len(redirect)
}

// buildCollapseRedirectMap scans edges for ExternalService → CloudResource
// bridge edges and returns a map from ES.ID to its redirect target. Multiple
// bridge edges on the same ES are not expected today; if encountered, the
// first one wins and the rest are logged.
func buildCollapseRedirectMap(
	nodes []*DbNode,
	edges []*DbEdge,
	logger *slog.Logger,
) map[string]collapseRedirectEntry {
	nodeByID := make(map[string]*DbNode, len(nodes))
	for _, n := range nodes {
		if n != nil {
			nodeByID[n.ID] = n
		}
	}

	redirect := make(map[string]collapseRedirectEntry)
	for _, e := range edges {
		if e == nil {
			continue
		}
		if e.RelationshipType != RelationshipRoutesThrough && e.RelationshipType != RelationshipResolvesTo {
			continue
		}
		src, ok := nodeByID[e.SourceNodeID]
		if !ok || src == nil || src.NodeType != NodeTypeExternalService {
			continue
		}
		if _, exists := redirect[e.SourceNodeID]; exists {
			logger.Warn("external service has multiple bridge edges; keeping first",
				"es_id", e.SourceNodeID,
				"es_unique_key", src.UniqueKey,
				"ignored_target", e.DestinationNodeID,
				"ignored_relationship", e.RelationshipType)
			continue
		}
		hostname, _ := src.Properties["name"].(string)
		redirect[e.SourceNodeID] = collapseRedirectEntry{
			targetID:         e.DestinationNodeID,
			originalHostname: hostname,
			originalUniqKey:  src.UniqueKey,
		}
	}
	return redirect
}

// rewriteEdgesForCollapse produces the post-collapse edge slice given the
// redirect map. Returns the new slice and aggregate counters for logging.
func rewriteEdgesForCollapse(
	edges []*DbEdge,
	redirect map[string]collapseRedirectEntry,
	logger *slog.Logger,
) ([]*DbEdge, collapseStats) {
	out := make([]*DbEdge, 0, len(edges))
	var stats collapseStats

	for _, e := range edges {
		if e == nil {
			continue
		}
		kept, action := classifyAndRewriteEdgeForCollapse(e, redirect, logger)
		switch action {
		case collapseActionDropBridge:
			stats.droppedBridges++
		case collapseActionDropOther:
			stats.droppedOther++
		case collapseActionRewroteCalls:
			stats.rewrittenCalls++
		}
		if kept {
			out = append(out, e)
		}
	}
	return out, stats
}

type collapseAction int

const (
	collapseActionKeepUnchanged collapseAction = iota
	collapseActionRewroteCalls
	collapseActionDropBridge
	collapseActionDropOther
)

// classifyAndRewriteEdgeForCollapse decides what to do with a single edge and
// mutates it in place when the action is a CALLS rewrite. Returns whether the
// edge should be kept and which action was taken.
func classifyAndRewriteEdgeForCollapse(
	e *DbEdge,
	redirect map[string]collapseRedirectEntry,
	logger *slog.Logger,
) (kept bool, action collapseAction) {
	if _, isRedirectedSource := redirect[e.SourceNodeID]; isRedirectedSource {
		if e.RelationshipType == RelationshipRoutesThrough || e.RelationshipType == RelationshipResolvesTo {
			return false, collapseActionDropBridge
		}
		logger.Warn("dropping unexpected outbound edge from redirected ExternalService",
			"es_id", e.SourceNodeID,
			"relationship", e.RelationshipType,
			"destination", e.DestinationNodeID)
		return false, collapseActionDropOther
	}

	entry, isRedirectedDest := redirect[e.DestinationNodeID]
	if !isRedirectedDest {
		return true, collapseActionKeepUnchanged
	}
	if e.RelationshipType != RelationshipCalls {
		logger.Warn("dropping unexpected inbound edge to redirected ExternalService",
			"es_id", e.DestinationNodeID,
			"relationship", e.RelationshipType,
			"source", e.SourceNodeID)
		return false, collapseActionDropOther
	}

	if e.Properties == nil {
		e.Properties = make(map[string]interface{})
	}
	e.Properties[CollapseEdgePropOriginalHostname] = entry.originalHostname
	e.Properties[CollapseEdgePropOriginalESUniqueKey] = entry.originalUniqKey
	e.DestinationNodeID = entry.targetID
	return true, collapseActionRewroteCalls
}

// filterCollapsedNodes returns nodes minus any whose ID appears in the
// redirect map. Preserves order.
func filterCollapsedNodes(nodes []*DbNode, redirect map[string]collapseRedirectEntry) []*DbNode {
	out := make([]*DbNode, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if _, isRedirected := redirect[n.ID]; isRedirected {
			continue
		}
		out = append(out, n)
	}
	return out
}
