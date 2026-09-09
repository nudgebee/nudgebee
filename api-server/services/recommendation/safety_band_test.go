package recommendation

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// The ChangeClassUnknown cases double as the regression suite for the
// pre-change-aware policy: an unclassified rule must grade exactly as before.
func TestDeriveSafetyBand(t *testing.T) {
	tests := []struct {
		name   string
		impact *core.ImpactSummary
		class  ChangeClass
		want   SafetyBand
	}{
		{
			name:   "nil impact is unknown",
			impact: nil,
			want:   SafetyBandUnknown,
		},
		{
			name:   "seed absent from graph is unknown, never safe",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageNone},
			want:   SafetyBandUnknown,
		},
		{
			name:   "seed absent stays unknown even for a destructive change",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageNone},
			class:  ChangeClassDestructive,
			want:   SafetyBandUnknown,
		},
		{
			name:   "production dependents are risky",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 4, ProductionDependents: 2},
			want:   SafetyBandRisky,
		},
		{
			name:   "truncated blast radius is risky even without prod flag",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 500, Truncated: true},
			want:   SafetyBandRisky,
		},
		{
			name:   "zero dependents with high coverage is safe",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 0},
			want:   SafetyBandSafe,
		},
		{
			name:   "zero dependents but low coverage is review, not safe",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageLow, DependentCount: 0},
			want:   SafetyBandReview,
		},
		{
			name:   "zero dependents with observed coverage is safe (single-source tenant)",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageObserved, DependentCount: 0},
			want:   SafetyBandSafe,
		},
		{
			name:   "dependents with observed coverage are still review",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageObserved, DependentCount: 2},
			want:   SafetyBandReview,
		},
		{
			name:   "non-production dependents are review",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageLow, DependentCount: 3, ProductionDependents: 0},
			want:   SafetyBandReview,
		},
		{
			name:   "reductive with production dependents is risky (matches unclassified)",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 4, ProductionDependents: 2},
			class:  ChangeClassReductive,
			want:   SafetyBandRisky,
		},
		{
			name:   "additive caps production dependents at review",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 14, ProductionDependents: 14},
			class:  ChangeClassAdditive,
			want:   SafetyBandReview,
		},
		{
			name:   "additive caps a truncated blast radius at review",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 500, Truncated: true},
			class:  ChangeClassAdditive,
			want:   SafetyBandReview,
		},
		{
			name:   "additive with zero dependents and high coverage stays safe",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 0},
			class:  ChangeClassAdditive,
			want:   SafetyBandSafe,
		},
		{
			name:   "additive with zero dependents but low coverage stays review",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageLow, DependentCount: 0},
			class:  ChangeClassAdditive,
			want:   SafetyBandReview,
		},
		{
			name:   "destructive with dependents is risky even when none are production",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 2, ProductionDependents: 0},
			class:  ChangeClassDestructive,
			want:   SafetyBandRisky,
		},
		{
			name:   "destructive with zero dependents but low coverage is risky, not review",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageLow, DependentCount: 0},
			class:  ChangeClassDestructive,
			want:   SafetyBandRisky,
		},
		{
			name:   "destructive with zero dependents and high coverage earns review, never safe",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 0},
			class:  ChangeClassDestructive,
			want:   SafetyBandReview,
		},
		{
			name:   "destructive stays risky while infrastructure is attached (a volume's instance)",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 0, InfrastructureCount: 1},
			class:  ChangeClassDestructive,
			want:   SafetyBandRisky,
		},
		{
			name:   "destructive stays risky while workloads are hosted (a node's rollup)",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 0, HostedWorkloadCount: 5},
			class:  ChangeClassDestructive,
			want:   SafetyBandRisky,
		},
		{
			name:   "destructive stays risky while the resource still fronts targets (an LB's backends)",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 0, DownstreamCount: 3},
			class:  ChangeClassDestructive,
			want:   SafetyBandRisky,
		},
		{
			name:   "reductive is unaffected by hosted workloads — callers only",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 0, HostedWorkloadCount: 12},
			class:  ChangeClassReductive,
			want:   SafetyBandSafe,
		},
		{
			name:   "destructive with zero dependents under an active signal earns review",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageObserved, DependentCount: 0},
			class:  ChangeClassDestructive,
			want:   SafetyBandReview,
		},
		{
			name:   "destructive truncated is risky regardless of prod counts",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageHigh, DependentCount: 500, Truncated: true},
			class:  ChangeClassDestructive,
			want:   SafetyBandRisky,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := DeriveSafetyBand(tt.impact, tt.class)
			if got != tt.want {
				t.Errorf("DeriveSafetyBand() = %q, want %q", got, tt.want)
			}
			if reason == "" {
				t.Errorf("DeriveSafetyBand() returned empty reason for %q", got)
			}
		})
	}
}
