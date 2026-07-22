package recommendation

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestDeriveSafetyBand(t *testing.T) {
	tests := []struct {
		name   string
		impact *core.ImpactSummary
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
			name:   "non-production dependents are review",
			impact: &core.ImpactSummary{CoverageConfidence: core.CoverageLow, DependentCount: 3, ProductionDependents: 0},
			want:   SafetyBandReview,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := DeriveSafetyBand(tt.impact)
			if got != tt.want {
				t.Errorf("DeriveSafetyBand() = %q, want %q", got, tt.want)
			}
			if reason == "" {
				t.Errorf("DeriveSafetyBand() returned empty reason for %q", got)
			}
		})
	}
}
