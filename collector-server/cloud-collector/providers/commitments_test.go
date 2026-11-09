package providers

import (
	"testing"
	"time"

	"nudgebee/collector/cloud/providers/constants"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var commitmentTestNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func testCommitment(id string, endsIn time.Duration) Commitment {
	return Commitment{
		ID:        id,
		Provider:  constants.ProviderAWS,
		AccountID: "123456789012",
		Type:      CommitmentTypeSavingsPlan,
		StartTime: commitmentTestNow.AddDate(-1, 0, 0),
		EndTime:   commitmentTestNow.Add(endsIn),
		Status:    "active",
	}
}

func testUtilization(id string, pct float64) Utilization {
	return Utilization{
		Provider:       constants.ProviderAWS,
		CommitmentID:   id,
		CommitmentType: CommitmentTypeSavingsPlan,
		UtilizationPct: pct,
		CommittedUsage: 7200,
		UsedUsage:      72 * pct,
		UnusedUsage:    7200 - 72*pct,
		UsageUnit:      "USD",
		UnusedCost:     7200 - 72*pct,
		Currency:       "USD",
		StartTime:      commitmentTestNow.AddDate(0, 0, -30),
		EndTime:        commitmentTestNow,
	}
}

func recommendationsByRule(recommendations []Recommendation) map[string]Recommendation {
	byRule := map[string]Recommendation{}
	for _, r := range recommendations {
		byRule[r.RuleName] = r
	}
	return byRule
}

const day = 24 * time.Hour

func TestCommitmentRecommendations_Utilization(t *testing.T) {
	tests := []struct {
		name         string
		pct          float64
		wantSeverity RecommendationSeverity // empty: no recommendation
	}{
		{"fully used", 100, ""},
		{"at the medium threshold", 80, ""},
		{"just under the medium threshold", 79.9, RecommendationSeverityMedium},
		{"at the high threshold", 50, RecommendationSeverityMedium},
		{"just under the high threshold", 49.9, RecommendationSeverityHigh},
		{"unused", 0, RecommendationSeverityHigh},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CommitmentRecommendations(
				[]Commitment{testCommitment("sp-1", 200*day)},
				[]Utilization{testUtilization("sp-1", tt.pct)},
				commitmentTestNow,
			)
			if tt.wantSeverity == "" {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, constants.UnderutilizedCommitment, got[0].RuleName)
			assert.Equal(t, tt.wantSeverity, got[0].Severity)
		})
	}
}

func TestCommitmentRecommendations_Expiry(t *testing.T) {
	tests := []struct {
		name         string
		endsIn       time.Duration
		wantSeverity RecommendationSeverity // empty: no recommendation
		wantDays     int
	}{
		{"31 days left", 31 * day, "", 0},
		{"30 days left", 30 * day, RecommendationSeverityHigh, 30},
		{"8 days left", 8 * day, RecommendationSeverityHigh, 8},
		{"7 days left", 7 * day, RecommendationSeverityCritical, 7},
		{"1 day left", 1 * day, RecommendationSeverityCritical, 1},
		{"ends later today", 3 * time.Hour, RecommendationSeverityCritical, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CommitmentRecommendations([]Commitment{testCommitment("sp-1", tt.endsIn)}, nil, commitmentTestNow)
			if tt.wantSeverity == "" {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, constants.CommitmentExpiringSoon, got[0].RuleName)
			assert.Equal(t, tt.wantSeverity, got[0].Severity)
			assert.Equal(t, tt.wantDays, got[0].Data["days_remaining"])
			assert.Equal(t, commitmentTestNow.Add(tt.endsIn).Format(time.RFC3339), got[0].Data["expiry_date"])
		})
	}
}

// Providers keep reporting utilization for a commitment while it is inside the
// lookback window, so an ended one must not be flagged by any rule.
func TestCommitmentRecommendations_EndedCommitmentYieldsNothing(t *testing.T) {
	for _, endsIn := range []time.Duration{0, -time.Hour, -10 * day} {
		got := CommitmentRecommendations(
			[]Commitment{testCommitment("sp-1", endsIn)},
			[]Utilization{testUtilization("sp-1", 10)},
			commitmentTestNow,
		)
		assert.Empty(t, got, "ends in %s", endsIn)
	}
}

func TestCommitmentRecommendations_UnderusedAndExpiring(t *testing.T) {
	got := CommitmentRecommendations(
		[]Commitment{testCommitment("sp-1", 25*day)},
		[]Utilization{testUtilization("sp-1", 60)},
		commitmentTestNow,
	)
	require.Len(t, got, 2)
	byRule := recommendationsByRule(got)

	underused := byRule[constants.UnderutilizedCommitment]
	assert.Equal(t, RecommendationSeverityMedium, underused.Severity)
	assert.Equal(t, "sp-1", underused.Data["commitment_id"])
	assert.Equal(t, CommitmentTypeSavingsPlan, underused.Data["commitment_type"])
	assert.Equal(t, 60.0, underused.Data["utilization_percentage"])
	assert.Equal(t, 7200.0, underused.Data["total_commitment"])
	assert.Equal(t, 4320.0, underused.Data["used_commitment"])
	assert.Equal(t, 2880.0, underused.Data["unused_commitment"])
	assert.Equal(t, 2880.0, underused.Data["unused_cost"])
	assert.Equal(t, "USD", underused.Data["currency"])
	assert.Equal(t, 30, underused.Data["window_days"])
	assert.NotContains(t, underused.Data, "days_remaining")

	// The expiry row carries the utilization too: it is what sizes the renewal.
	expiring := byRule[constants.CommitmentExpiringSoon]
	assert.Equal(t, RecommendationSeverityHigh, expiring.Severity)
	assert.Equal(t, 25, expiring.Data["days_remaining"])
	assert.Equal(t, 60.0, expiring.Data["utilization_percentage"])
}

// The fields below decide which row a finding lands on and which rows a sync
// archives, so they must not drift between runs or between rules.
func TestCommitmentRecommendations_Identity(t *testing.T) {
	got := CommitmentRecommendations(
		[]Commitment{testCommitment("sp-1", 5*day)},
		[]Utilization{testUtilization("sp-1", 10)},
		commitmentTestNow,
	)
	require.Len(t, got, 2)
	for _, r := range got {
		assert.Equal(t, RecommendationCategoryRightSizing, r.CategoryName)
		assert.Equal(t, RecommendationActionModify, r.Action)
		assert.Equal(t, CommitmentServiceName, r.ResourceServiceName)
		assert.Equal(t, "sp-1", r.ResourceId)
		assert.Equal(t, "savings-plan", r.ResourceType)
		assert.Equal(t, "global", r.ResourceRegion)
		assert.Empty(t, r.ExternalResourceId)
		assert.Empty(t, r.DedupeGroup)
		assert.Zero(t, r.Savings)
	}
}

func TestCommitmentRecommendations_UtilizationEdgeCases(t *testing.T) {
	t.Run("utilization for another commitment is not applied", func(t *testing.T) {
		got := CommitmentRecommendations(
			[]Commitment{testCommitment("sp-1", 200*day)},
			[]Utilization{testUtilization("sp-2", 10)},
			commitmentTestNow,
		)
		assert.Empty(t, got)
	})

	t.Run("nothing committed in the window is not a finding", func(t *testing.T) {
		utilization := testUtilization("sp-1", 0)
		utilization.CommittedUsage = 0
		got := CommitmentRecommendations([]Commitment{testCommitment("sp-1", 200*day)}, []Utilization{utilization}, commitmentTestNow)
		assert.Empty(t, got)
	})

	t.Run("no monetary figure leaves waste out of the payload", func(t *testing.T) {
		utilization := testUtilization("ri-1", 40)
		utilization.UsageUnit = "HOURS"
		utilization.Currency = ""
		got := CommitmentRecommendations([]Commitment{testCommitment("ri-1", 200*day)}, []Utilization{utilization}, commitmentTestNow)
		require.Len(t, got, 1)
		assert.Equal(t, "HOURS", got[0].Data["usage_unit"])
		assert.NotContains(t, got[0].Data, "unused_cost")
		assert.NotContains(t, got[0].Data, "currency")
	})

	t.Run("unknown end date still reports utilization", func(t *testing.T) {
		commitment := testCommitment("sp-1", 0)
		commitment.EndTime = time.Time{}
		got := CommitmentRecommendations([]Commitment{commitment}, []Utilization{testUtilization("sp-1", 10)}, commitmentTestNow)
		require.Len(t, got, 1)
		assert.Equal(t, constants.UnderutilizedCommitment, got[0].RuleName)
	})
}

func testCoverage(service string, covered, total float64) Coverage {
	return Coverage{
		Provider:       constants.ProviderAWS,
		CommitmentType: CommitmentTypeSavingsPlan,
		AccountID:      "123456789012",
		Service:        service,
		CoveragePct:    covered / total * 100,
		CoveredUsage:   covered,
		TotalUsage:     total,
		UsageUnit:      "USD",
		StartTime:      commitmentTestNow.AddDate(0, 0, -30),
		EndTime:        commitmentTestNow,
	}
}

func TestCoverageRecommendations(t *testing.T) {
	tests := []struct {
		name         string
		covered      float64
		total        float64
		wantSeverity RecommendationSeverity // empty: no recommendation
	}{
		{"fully covered", 1000, 1000, ""},
		{"at the medium threshold", 700, 1000, ""},
		{"just under the medium threshold", 699, 1000, RecommendationSeverityMedium},
		{"at the high threshold", 400, 1000, RecommendationSeverityMedium},
		{"just under the high threshold", 399, 1000, RecommendationSeverityHigh},
		{"nothing covered", 0, 1000, RecommendationSeverityHigh},
		{"nothing covered but too little uncovered usage to matter", 0, 99, ""},
		{"no eligible usage", 0, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coverage := testCoverage("Amazon Elastic Compute Cloud - Compute", tt.covered, 1)
			coverage.TotalUsage = tt.total
			got := CoverageRecommendations([]Coverage{coverage}, []Commitment{testCommitment("sp-1", 200*day)})
			if tt.wantSeverity == "" {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, constants.LowCommitmentCoverage, got[0].RuleName)
			assert.Equal(t, tt.wantSeverity, got[0].Severity)
		})
	}
}

// One row per commitment type for the whole account, judged on the summed usage
// rather than on any one service, with the services listed worst-first.
func TestCoverageRecommendations_AcrossServices(t *testing.T) {
	got := CoverageRecommendations([]Coverage{
		testCoverage("AWS Lambda", 90, 100),
		testCoverage("Amazon Elastic Compute Cloud - Compute", 510, 900),
	}, []Commitment{testCommitment("sp-1", 200*day)})
	require.Len(t, got, 1)
	r := got[0]
	assert.Equal(t, RecommendationSeverityMedium, r.Severity)
	assert.Equal(t, RecommendationCategoryRightSizing, r.CategoryName)
	assert.Equal(t, CommitmentServiceName, r.ResourceServiceName)
	assert.Equal(t, CommitmentTypeSavingsPlan, r.ResourceId)
	assert.Equal(t, "commitment-coverage", r.ResourceType)
	assert.Equal(t, "global", r.ResourceRegion)
	assert.Empty(t, r.DedupeGroup)
	assert.Zero(t, r.Savings)

	assert.InDelta(t, 60.0, r.Data["coverage_percentage"], 0.0001)
	assert.Equal(t, 600.0, r.Data["covered_usage"])
	assert.Equal(t, 1000.0, r.Data["total_usage"])
	assert.Equal(t, 400.0, r.Data["uncovered_usage"])
	assert.Equal(t, "USD", r.Data["usage_unit"])
	assert.Equal(t, 30, r.Data["window_days"])

	byService := r.Data["by_service"].([]map[string]any)
	require.Len(t, byService, 2)
	assert.Equal(t, "Amazon Elastic Compute Cloud - Compute", byService[0]["service"])
	assert.Equal(t, 390.0, byService[0]["uncovered_usage"])
	assert.Equal(t, "AWS Lambda", byService[1]["service"])
}

// An account that owns no commitment of the type is 0% covered by definition
// and is already told to buy one by the provider's purchase recommendations.
func TestCoverageRecommendations_OnlyForOwnedCommitmentTypes(t *testing.T) {
	uncovered := []Coverage{testCoverage("Amazon Elastic Compute Cloud - Compute", 0, 5000)}

	assert.Empty(t, CoverageRecommendations(uncovered, nil))

	reservedOnly := testCommitment("ri-1", 200*day)
	reservedOnly.Type = CommitmentTypeReservedInstance
	assert.Empty(t, CoverageRecommendations(uncovered, []Commitment{reservedOnly}))

	assert.Len(t, CoverageRecommendations(uncovered, []Commitment{reservedOnly, testCommitment("sp-1", 200*day)}), 1)
}

// A commitment's id is a UUID for most commitments, so the row also says what
// it covers and when it ends.
func TestCommitmentRecommendations_PayloadIdentifiesTheCommitment(t *testing.T) {
	commitment := testCommitment("0f1e2d3c-aaaa-bbbb-cccc-1234567890ab", 200*day)
	commitment.Resources = []CommitmentResource{{
		Type:         "instance",
		Quantity:     3,
		Unit:         "INSTANCES",
		Service:      "AmazonEC2",
		Region:       "us-east-1",
		ResourceType: "m5.large",
		Metadata:     map[string]any{"offering_class": "standard"},
	}}

	got := CommitmentRecommendations([]Commitment{commitment}, []Utilization{testUtilization(commitment.ID, 10)}, commitmentTestNow)
	require.Len(t, got, 1)
	data := got[0].Data
	assert.Equal(t, "AmazonEC2", data["service"])
	assert.Equal(t, "us-east-1", data["region"])
	assert.Equal(t, "m5.large", data["resource_type"])
	assert.Equal(t, 3.0, data["committed_quantity"])
	assert.Equal(t, "INSTANCES", data["committed_unit"])
	assert.Equal(t, map[string]any{"offering_class": "standard"}, data["details"])
	assert.Equal(t, commitment.EndTime.Format(time.RFC3339), data["expiry_date"])
	assert.NotContains(t, data, "resources")

	t.Run("a commitment covering several things lists each", func(t *testing.T) {
		commitment.Resources = append(commitment.Resources, CommitmentResource{Type: "memory", Quantity: 32768, Unit: "MB"})
		got := CommitmentRecommendations([]Commitment{commitment}, []Utilization{testUtilization(commitment.ID, 10)}, commitmentTestNow)
		require.Len(t, got, 1)
		resources := got[0].Data["resources"].([]map[string]any)
		require.Len(t, resources, 2)
		assert.Equal(t, "MB", resources[1]["committed_unit"])
		assert.NotContains(t, resources[1], "service")
	})

	t.Run("an empty field is left out rather than stored blank", func(t *testing.T) {
		plan := testCommitment("sp-1", 200*day)
		plan.Resources = []CommitmentResource{{Type: "spend", Quantity: 10, Unit: "USD_PER_HOUR", Service: "EC2,Fargate"}}
		got := CommitmentRecommendations([]Commitment{plan}, []Utilization{testUtilization("sp-1", 10)}, commitmentTestNow)
		require.Len(t, got, 1)
		assert.Equal(t, "EC2,Fargate", got[0].Data["service"])
		assert.NotContains(t, got[0].Data, "region")
		assert.NotContains(t, got[0].Data, "resource_type")
		assert.NotContains(t, got[0].Data, "details")
	})
}
