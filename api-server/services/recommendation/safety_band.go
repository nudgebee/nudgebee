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
	// SafetyBandRisky — production dependents, a very large blast radius, or an
	// irreversible change without verified-empty surroundings.
	SafetyBandRisky SafetyBand = "risky"
	// SafetyBandUnknown — the resource isn't in the dependency graph, so impact
	// is genuinely unknown. Never conflate this with "safe".
	SafetyBandUnknown SafetyBand = "unknown"
)

// DeriveSafetyBand maps a knowledge-graph ImpactSummary plus the
// recommendation's change class to a safety band: the verdict grades what the
// change does (additive / reductive / destructive), who relies on the resource,
// and how well the graph observed it. It is deliberately conservative: it never
// returns "safe" unless the graph actually observed the resource's
// neighbourhood, so a thin or absent graph downgrades to "review"/"unknown"
// rather than manufacturing false confidence. Only upstream dependents feed the
// band — DownstreamDependencies are operator context, not risk to callers.
//
// The class matrix:
//   - additive — cannot starve callers, so production dependents and a
//     truncated traversal cap at review instead of risky (the remaining risk is
//     apply mechanics, e.g. a rolling restart). Coverage still gates "safe".
//   - reductive / unknown — the pre-change-aware policy: production dependents
//     or truncation → risky. On an account marked prod
//     (cloud_accounts.account_env) every dependent resolves to production, so
//     any reductive recommendation with at least one dependent grades risky by
//     design — see the environment-derivation notes in the KG impact layer. If
//     band granularity inside prod accounts is ever needed, split label-derived
//     vs account-derived prod counts in the impact summary rather than
//     re-thresholding here.
//   - destructive — irreversible, so it floors at risky; a well-observed,
//     dependent-free neighbourhood earns review (one human glance before an
//     unrecoverable action), never safe.
func DeriveSafetyBand(impact *core.ImpactSummary, class ChangeClass) (SafetyBand, string) {
	if impact == nil || impact.CoverageConfidence == core.CoverageNone {
		return SafetyBandUnknown, "resource not found in the dependency graph; impact unknown"
	}

	switch class {
	case ChangeClassDestructive:
		return deriveDestructiveBand(impact)
	case ChangeClassAdditive:
		return deriveAdditiveBand(impact)
	default:
		return deriveReductiveBand(impact)
	}
}

// deriveReductiveBand is the pre-change-aware policy, applied to reductive and
// unclassified changes: taking capacity away from production callers is the
// canonical risky case.
func deriveReductiveBand(impact *core.ImpactSummary) (SafetyBand, string) {
	switch {
	case impact.ProductionDependents > 0:
		return SafetyBandRisky, fmt.Sprintf("%d production dependent(s) would be affected", impact.ProductionDependents)
	case impact.Truncated:
		return SafetyBandRisky, "very large blast radius (dependents exceeded the traversal cap)"
	case impact.DependentCount == 0:
		return zeroDependentBand(impact)
	default:
		return SafetyBandReview, fmt.Sprintf("%d dependent(s); none detected as production", impact.DependentCount)
	}
}

// deriveAdditiveBand caps at review: an increase-only change cannot take
// capacity from callers, so production dependents stop escalating to risky —
// but applying still restarts the workload, so dependents keep a human glance.
func deriveAdditiveBand(impact *core.ImpactSummary) (SafetyBand, string) {
	switch {
	case impact.ProductionDependents > 0:
		return SafetyBandReview, fmt.Sprintf("adds capacity only; %d production dependent(s) are unaffected by a larger allocation — the rolling restart is the remaining risk", impact.ProductionDependents)
	case impact.Truncated:
		return SafetyBandReview, "very large dependent set, but the change only adds capacity"
	case impact.DependentCount == 0:
		return zeroDependentBand(impact)
	default:
		return SafetyBandReview, fmt.Sprintf("adds capacity only; %d dependent(s) see just the rolling restart", impact.DependentCount)
	}
}

// deriveDestructiveBand floors at risky: removal is irreversible, so the
// verdict softens — and then only to review, never safe — solely when the
// ENTIRE well-observed neighbourhood is empty: no application callers, no
// attached infrastructure (a volume's instance), nothing hosted (a node's
// workloads), and nothing the resource fronts or serves (an LB's backends,
// reported downstream). Those extra lists deliberately do not feed
// DependentCount — they are not callers — but an irreversible action on a
// resource that still has anything hanging off it in any direction must stay
// red. Decision logged in docs/architecture-decisions.md.
func deriveDestructiveBand(impact *core.ImpactSummary) (SafetyBand, string) {
	wellObserved := impact.CoverageConfidence == core.CoverageHigh || impact.CoverageConfidence == core.CoverageObserved
	neighborhoodEmpty := impact.DependentCount == 0 &&
		impact.InfrastructureCount == 0 &&
		impact.HostedWorkloadCount == 0 &&
		impact.DownstreamCount == 0
	if neighborhoodEmpty && !impact.Truncated && wellObserved {
		return SafetyBandReview, "nothing attached, hosted, or depending on this resource — but the change is irreversible; verify before removing"
	}
	switch {
	case impact.ProductionDependents > 0:
		return SafetyBandRisky, fmt.Sprintf("irreversible change with %d production dependent(s)", impact.ProductionDependents)
	case impact.Truncated:
		return SafetyBandRisky, "irreversible change with a very large blast radius"
	case impact.DependentCount > 0:
		return SafetyBandRisky, fmt.Sprintf("irreversible change with %d dependent(s)", impact.DependentCount)
	case impact.HostedWorkloadCount > 0:
		return SafetyBandRisky, fmt.Sprintf("irreversible change on a node hosting %d workload(s)", impact.HostedWorkloadCount)
	case impact.InfrastructureCount > 0:
		return SafetyBandRisky, fmt.Sprintf("irreversible change with %d attached infrastructure resource(s)", impact.InfrastructureCount)
	case impact.DownstreamCount > 0:
		return SafetyBandRisky, fmt.Sprintf("irreversible change on a resource still fronting or serving %d target(s)", impact.DownstreamCount)
	default:
		return SafetyBandRisky, "irreversible change and graph coverage is too limited to confirm nothing depends on it"
	}
}

// zeroDependentBand grades the no-dependents case on coverage alone — shared by
// the additive and reductive matrices.
func zeroDependentBand(impact *core.ImpactSummary) (SafetyBand, string) {
	switch impact.CoverageConfidence {
	case core.CoverageHigh:
		return SafetyBandSafe, "no known dependents (well-observed in the graph)"
	case core.CoverageObserved:
		return SafetyBandSafe, "no dependents seen by an active traffic signal watching this scope"
	default:
		return SafetyBandReview, "no dependents observed, but graph coverage is limited"
	}
}
