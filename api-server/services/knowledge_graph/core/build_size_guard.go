package core

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/security"
)

// ErrBuildTooLarge marks a build that was stopped because its graph grew past
// kg_build_max_nodes or kg_build_max_edges.
//
// BuildGraphs holds a tenant's whole graph in memory, and the pod that runs it
// also serves API traffic. In #40937 one tenant reached 731k nodes / 1.48M edges
// and OOMKilled services-server every hour. Stopping the build turns that into a
// failure of one tenant's build: nothing is saved, nothing is swept, the previous
// graph stays served, and the queue records the build request as failed with the
// summary below as its cause.
//
// A build is never truncated to fit. Saving part of a graph would let the
// inactive-node sweep tombstone everything that was cut off.
var ErrBuildTooLarge = errors.New("knowledge graph build exceeds size limit")

// Used when kg_build_max_nodes / kg_build_max_edges are unset or non-positive.
// Keep in step with the viper defaults in config/config.go.
//
// Calibration (test/build_memory_measure_live_test.go, dev, 2026-10-13): a real
// build of a 9,456-node / 15,201-edge tenant peaked at 125.6 MB of heap, plus
// 16.3 MB for the JSON the save marshals up front — about 15.7 KB per node
// all-in, matching the read-path benchmark in test/KG_SCALING_BASELINE.md. The
// pod is 4Gi and also serves API traffic, so builds get half of it, and two can
// run at once (rabbit_mq_kg_update_concurrency defaults to 2): 1 GiB per build
// ÷ 15.7 KB ≈ 68k nodes. Edges cost roughly a tenth of a node, and 5× the node
// cap covers the most edge-heavy tenant seen (3.5 edges per node).
//
// The largest healthy tenant seen is ~17k nodes, so this leaves about 4×
// headroom. Erring low is deliberate: a tenant that trips it keeps its previous
// graph and is named in the abort, while one that slips past OOMs the API pod.
const (
	defaultBuildMaxNodes = 68000
	defaultBuildMaxEdges = 340000
)

// buildSizeTopContributors is how many (account, source) pairs the abort names.
const buildSizeTopContributors = 5

// sourceContribution is what one source added to the in-memory graph: one
// account's source in Phase 1, a tenant-scoped source in Phase 1b, the context
// load of a partial build, or one cross-source enricher.
type sourceContribution struct {
	AccountID   string
	AccountName string
	Source      string
	Nodes       int
	Edges       int
}

func (c sourceContribution) String() string {
	who := c.Source
	switch {
	case c.AccountName != "":
		who = c.AccountName + " / " + c.Source
	case c.AccountID != "":
		who = c.AccountID + " / " + c.Source
	}
	return fmt.Sprintf("%s: %d nodes, %d edges", who, c.Nodes, c.Edges)
}

// buildSizeLimits returns the configured caps, falling back to the defaults for
// unset or non-positive values.
func buildSizeLimits() (maxNodes, maxEdges int) {
	maxNodes, maxEdges = config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges
	if maxNodes <= 0 {
		maxNodes = defaultBuildMaxNodes
	}
	if maxEdges <= 0 {
		maxEdges = defaultBuildMaxEdges
	}
	return maxNodes, maxEdges
}

// topContributors returns the n largest contributions, by nodes then edges.
func topContributors(contribs []sourceContribution, n int) []sourceContribution {
	sorted := make([]sourceContribution, len(contribs))
	copy(sorted, contribs)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Nodes != sorted[j].Nodes {
			return sorted[i].Nodes > sorted[j].Nodes
		}
		return sorted[i].Edges > sorted[j].Edges
	})
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// buildSizeError returns nil while the graph is within both caps, and otherwise
// an error wrapping ErrBuildTooLarge that names the stage, both counts against
// their caps, and the largest contributors — enough for whoever reads the failed
// build request to know which account and source to look at.
func buildSizeError(tenantID, stage string, nodes, edges, maxNodes, maxEdges int, contribs []sourceContribution) error {
	if nodes <= maxNodes && edges <= maxEdges {
		return nil
	}
	top := topContributors(contribs, buildSizeTopContributors)
	parts := make([]string, len(top))
	for i, c := range top {
		parts[i] = c.String()
	}
	return fmt.Errorf("%w: tenant %s after %s: %d nodes (max %d), %d edges (max %d); largest: %s",
		ErrBuildTooLarge, tenantID, stage, nodes, maxNodes, edges, maxEdges, strings.Join(parts, "; "))
}

// enforceBuildSize stops the build when the in-memory graph has outgrown the
// configured caps. stage says where in BuildGraphs the check ran.
func (s *Service) enforceBuildSize(ctx *security.RequestContext, tenantID, stage string, nodes, edges int, contribs []sourceContribution) error {
	maxNodes, maxEdges := buildSizeLimits()
	err := buildSizeError(tenantID, stage, nodes, edges, maxNodes, maxEdges, contribs)
	if err == nil {
		return nil
	}

	top := topContributors(contribs, buildSizeTopContributors)
	largest := make([]string, len(top))
	for i, c := range top {
		largest[i] = c.String()
	}
	s.logger.Error("knowledge graph build aborted: graph exceeds size limit",
		"tenant_id", tenantID,
		"stage", stage,
		"nodes", nodes,
		"max_nodes", maxNodes,
		"edges", edges,
		"max_edges", maxEdges,
		"largest_contributors", largest)
	common.MetricsKGBuildAborted(ctx.GetContext(), tenantID, "too_large")
	return err
}
