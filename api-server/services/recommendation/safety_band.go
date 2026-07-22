package recommendation

import (
	"fmt"

	"nudgebee/services/knowledge_graph/core"
)

// SafetyBand is the user-facing verdict on how safe it is to act on a
// recommendation, derived from the knowledge-graph blast radius. It is product
// policy (deliberately kept out of the graph core) and is the gate the @finops
// agent consults before applying a change.
type SafetyBand string

const (
	// SafetyBandSafe — no known dependents and the graph is well observed.
	SafetyBandSafe SafetyBand = "safe"
	// SafetyBandReview — has dependents (none detected as production), or zero
	// dependents but limited graph coverage; a human should glance before acting.
	SafetyBandReview SafetyBand = "review"
	// SafetyBandRisky — production dependents, or a very large blast radius.
	SafetyBandRisky SafetyBand = "risky"
	// SafetyBandUnknown — the resource isn't in the dependency graph, so impact
	// is genuinely unknown. Never conflate this with "safe".
	SafetyBandUnknown SafetyBand = "unknown"
)

// DeriveSafetyBand maps a knowledge-graph ImpactSummary to a safety band. It is
// deliberately conservative: it never returns "safe" unless the graph actually
// observed the resource's neighbourhood, so a thin or absent graph downgrades to
// "review"/"unknown" rather than manufacturing false confidence.
func DeriveSafetyBand(impact *core.ImpactSummary) (SafetyBand, string) {
	if impact == nil || impact.CoverageConfidence == core.CoverageNone {
		return SafetyBandUnknown, "resource not found in the dependency graph; impact unknown"
	}

	switch {
	case impact.ProductionDependents > 0:
		return SafetyBandRisky, fmt.Sprintf("%d production dependent(s) would be affected", impact.ProductionDependents)
	case impact.Truncated:
		return SafetyBandRisky, "very large blast radius (dependents exceeded the traversal cap)"
	case impact.DependentCount == 0:
		if impact.CoverageConfidence == core.CoverageHigh {
			return SafetyBandSafe, "no known dependents (well-observed in the graph)"
		}
		return SafetyBandReview, "no dependents observed, but graph coverage is limited"
	default:
		return SafetyBandReview, fmt.Sprintf("%d dependent(s); none detected as production", impact.DependentCount)
	}
}
