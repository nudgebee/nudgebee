package recommendation

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func sevPtr(s string) *string { return &s }

func createdHoursAgo(h float64) *time.Time {
	t := time.Now().Add(-time.Duration(h * float64(time.Hour)))
	return &t
}

// old enough that the recency boost is zero.
func createdLongAgo() *time.Time { return createdHoursAgo(100 * 24) }

func TestGetSavingsScore(t *testing.T) {
	cases := []struct {
		savings float32
		want    int
	}{
		{0, 0},
		{-5, 0},
		{2, 12},
		{15, 32},
		{150, 58},
		{500, 72},
		{665, 76},
		{5000, 100},
		{50000, 100},
	}
	for _, c := range cases {
		if got := getSavingsScore(c.savings); got != c.want {
			t.Errorf("getSavingsScore(%v) = %d, want %d", c.savings, got, c.want)
		}
	}
}

func TestGetRecencyBoost(t *testing.T) {
	if got := getRecencyBoost(nil); got != 0 {
		t.Errorf("getRecencyBoost(nil) = %d, want 0", got)
	}
	cases := []struct {
		hoursAgo float64
		want     int
	}{
		{12, 8},
		{3 * 24, 5},
		{15 * 24, 2},
		{100 * 24, 0},
	}
	for _, c := range cases {
		if got := getRecencyBoost(createdHoursAgo(c.hoursAgo)); got != c.want {
			t.Errorf("getRecencyBoost(%.0fh ago) = %d, want %d", c.hoursAgo, got, c.want)
		}
	}
}

// TestComputeFinOpsScore_CategoryWeights pins the cross-category exchange
// rate: Critical config (70) ≈ a $600/mo cost rec, High config (53) sits
// below a $150/mo cost rec (56), performance keeps most of its severity.
func TestComputeFinOpsScore_CategoryWeights(t *testing.T) {
	cases := []struct {
		name      string
		category  string
		ruleName  string
		severity  string
		savings   float32
		wantScore int
		wantBand  string
	}{
		{"critical config", "Configuration", "misconfigurations", "Critical", 0, 70, "High"},
		{"high config", "Configuration", "misconfigurations", "High", 0, 53, "Medium"},
		{"critical security", "Security", "image_scan", "Critical", 0, 100, "Act Now"},
		{"critical performance", "InfraUpgrade", "eks_cluster_upgrade", "Critical", 0, 90, "Act Now"},
		{"perf rule override under config category", "Configuration", "pod_oom_killed", "High", 0, 68, "High"},
		{"cost $578 medium", "RightSizing", "ri_purchase", "Medium", 578, 69, "High"},
		{"cost $150 medium", "RightSizing", "ri_purchase", "Medium", 150, 56, "High"},
		{"cost $5000 medium", "RightSizing", "ri_purchase", "Medium", 5000, 90, "Act Now"},
	}
	for _, c := range cases {
		got := ComputeFinOpsScore(c.category, c.ruleName, sevPtr(c.severity), c.savings, createdLongAgo())
		if got.Score != c.wantScore || got.Band != c.wantBand {
			t.Errorf("%s: got score=%d band=%q, want score=%d band=%q",
				c.name, got.Score, got.Band, c.wantScore, c.wantBand)
		}
	}
}

// Cost recs with no positive savings are reliability recommendations
// ("increase resources") and must score on the performance scale instead of
// being zeroed by the savings term.
func TestComputeFinOpsScore_ReliabilityReclassification(t *testing.T) {
	// Critical under-provisioned pod: perf 90 + auto-fix 5.
	got := ComputeFinOpsScore("RightSizing", "pod_right_sizing", sevPtr("Critical"), 0, createdLongAgo())
	if got.Score != 95 || got.Band != "Act Now" {
		t.Errorf("critical reliability rightsizing: got score=%d band=%q, want 95 / Act Now", got.Score, got.Band)
	}
	if got.Breakdown["nfs_category"] != NFSCategoryCost || got.Breakdown["scored_as"] != NFSCategoryPerformance {
		t.Errorf("breakdown categories = %v / %v, want cost / performance",
			got.Breakdown["nfs_category"], got.Breakdown["scored_as"])
	}

	// Negative savings (spend more for reliability): perf 68 + auto-fix 5.
	got = ComputeFinOpsScore("RightSizing", "pv_rightsize", sevPtr("High"), -10, createdLongAgo())
	if got.Score != 73 || got.Band != "High" {
		t.Errorf("negative-savings rightsizing: got score=%d band=%q, want 73 / High", got.Score, got.Band)
	}

	// Positive savings keeps the cost path.
	got = ComputeFinOpsScore("RightSizing", "ri_purchase", sevPtr("Medium"), 578, createdLongAgo())
	if got.Breakdown["scored_as"] != NFSCategoryCost {
		t.Errorf("positive-savings rec scored_as = %v, want cost", got.Breakdown["scored_as"])
	}
}

func TestComputeFinOpsScore_RecencyBoost(t *testing.T) {
	old := ComputeFinOpsScore("Configuration", "misconfigurations", sevPtr("Critical"), 0, createdLongAgo())
	fresh := ComputeFinOpsScore("Configuration", "misconfigurations", sevPtr("Critical"), 0, createdHoursAgo(12))
	if old.Score != 70 || fresh.Score != 78 {
		t.Errorf("recency boost: old=%d fresh=%d, want 70 / 78", old.Score, fresh.Score)
	}
	if fresh.Band != "Critical" {
		t.Errorf("fresh critical config band = %q, want Critical", fresh.Band)
	}
}

func TestComputeFinOpsScore_ClampAndNilInputs(t *testing.T) {
	// 100 base + 8 recency clamps at 100.
	got := ComputeFinOpsScore("Security", "image_scan", sevPtr("Critical"), 0, createdHoursAgo(12))
	if got.Score != 100 {
		t.Errorf("clamp: got %d, want 100", got.Score)
	}

	// nil severity defaults to 50, nil createdAt gets no boost.
	got = ComputeFinOpsScore("Configuration", "misconfigurations", nil, 0, nil)
	if got.Score != 35 {
		t.Errorf("nil inputs: got %d, want 35", got.Score)
	}
}

// Non-finite savings must sanitize to 0 (reliability path) and never reach the
// breakdown, where NaN/Inf would fail json.Marshal and abort upsert batches.
func TestComputeFinOpsScore_NonFiniteSavings(t *testing.T) {
	for _, savings := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		got := ComputeFinOpsScore("RightSizing", "ri_purchase", sevPtr("Medium"), savings, createdLongAgo())
		if got.Score != 45 || got.Breakdown["scored_as"] != NFSCategoryPerformance {
			t.Errorf("savings %v: got score=%d scored_as=%v, want 45 / performance", savings, got.Score, got.Breakdown["scored_as"])
		}
		if _, err := json.Marshal(got.Breakdown); err != nil {
			t.Errorf("savings %v: breakdown does not marshal: %v", savings, err)
		}
	}
}

func TestComputeFinOpsScore_BreakdownShape(t *testing.T) {
	got := ComputeFinOpsScore("RightSizing", "ri_purchase", sevPtr("Medium"), 578, createdLongAgo())
	if got.Breakdown["version"] != "v1" {
		t.Errorf("breakdown version = %v, want v1", got.Breakdown["version"])
	}
	factors, ok := got.Breakdown["factors"].(map[string]any)
	if !ok {
		t.Fatalf("breakdown factors missing")
	}
	for _, key := range []string{"severity", "severity_score", "recency_days", "recency_boost", "estimated_savings", "savings_score"} {
		if _, present := factors[key]; !present {
			t.Errorf("breakdown factors missing key %q", key)
		}
	}
}

func TestGetBand(t *testing.T) {
	cases := []struct {
		score int
		want  string
	}{
		{100, "Act Now"}, {90, "Act Now"},
		{89, "Critical"}, {75, "Critical"},
		{74, "High"}, {55, "High"},
		{54, "Medium"}, {35, "Medium"},
		{34, "Low"}, {0, "Low"},
	}
	for _, c := range cases {
		if got := GetBand(c.score); got != c.want {
			t.Errorf("GetBand(%d) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestGetNFSCategory(t *testing.T) {
	cases := []struct {
		category string
		ruleName string
		want     string
	}{
		{"RightSizing", "ri_purchase", NFSCategoryCost},
		{"Configuration", "pod_oom_killed", NFSCategoryPerformance}, // rule override wins
		{"Configuration", "misconfigurations", NFSCategoryConfig},
		{"Security", "image_scan", NFSCategorySecurity},
		{"InfraUpgrade", "anything", NFSCategoryPerformance},
		{"UnknownCategory", "unknown_rule", NFSCategoryConfig}, // default
	}
	for _, c := range cases {
		if got := GetNFSCategory(c.category, c.ruleName); got != c.want {
			t.Errorf("GetNFSCategory(%q, %q) = %q, want %q", c.category, c.ruleName, got, c.want)
		}
	}
}
