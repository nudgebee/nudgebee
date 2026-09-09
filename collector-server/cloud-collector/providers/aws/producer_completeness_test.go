package aws

import (
	"strings"
	"testing"

	"nudgebee/collector/cloud/providers"
)

func TestRegionsFromResourcesAlwaysIncludesDefault(t *testing.T) {
	tests := []struct {
		name      string
		resources []providers.Resource
		def       string
		want      []string
	}{
		{
			name: "no resources falls back to the account region",
			def:  "us-east-1",
			want: []string{"us-east-1"},
		},
		{
			name:      "discovered regions are unioned with the default, not replaced by it",
			resources: []providers.Resource{{Region: "ap-south-1"}, {Region: "eu-west-1"}},
			def:       "us-east-1",
			want:      []string{"us-east-1", "ap-south-1", "eu-west-1"},
		},
		{
			name:      "duplicates collapse",
			resources: []providers.Resource{{Region: "ap-south-1"}, {Region: "ap-south-1"}, {Region: "us-east-1"}},
			def:       "us-east-1",
			want:      []string{"us-east-1", "ap-south-1"},
		},
		{
			name:      "resources with no region are ignored",
			resources: []providers.Resource{{Region: ""}, {Region: "ap-south-1"}},
			def:       "us-east-1",
			want:      []string{"us-east-1", "ap-south-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := regionsFromResources(tt.resources, tt.def)
			if len(got) != len(tt.want) {
				t.Fatalf("regionsFromResources() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("regionsFromResources() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestRiRecommendationIdIsStableAndDistinct(t *testing.T) {
	first := map[string]any{"region": "ap-south-1", "instance_type": "m5.xlarge", "platform": "Linux/UNIX"}
	second := map[string]any{"region": "ap-south-1", "instance_type": "r5.2xlarge", "platform": "Linux/UNIX"}

	// The bug being guarded: both of these arrived at index 0 of different
	// elements of output.Recommendations, so both were "ce-ri-<service>-0".
	if a, b := riRecommendationId("EC2", first, 0), riRecommendationId("EC2", second, 0); a == b {
		t.Fatalf("different purchases share an id: %s", a)
	}

	// The same purchase must keep its id when AWS reorders the response.
	if a, b := riRecommendationId("EC2", first, 0), riRecommendationId("EC2", first, 7); a != b {
		t.Fatalf("id moved with response position: %s != %s", a, b)
	}

	// Attribute order in the map must not leak into the id.
	reordered := map[string]any{"platform": "Linux/UNIX", "instance_type": "m5.xlarge", "region": "ap-south-1"}
	if a, b := riRecommendationId("EC2", first, 0), riRecommendationId("EC2", reordered, 0); a != b {
		t.Fatalf("id depends on map iteration order: %s != %s", a, b)
	}

	// Different services never collide even on identical attributes.
	if a, b := riRecommendationId("EC2", first, 0), riRecommendationId("RDS", first, 0); a == b {
		t.Fatalf("services share an id: %s", a)
	}
}

func TestRiRecommendationIdFallsBackWhenNothingIdentifies(t *testing.T) {
	// A detail with no usable attributes still has to be addressable; the index
	// is a poor identity but better than every such row colliding on one id.
	id := riRecommendationId("EC2", map[string]any{"estimated_monthly_savings": 12.5}, 3)
	if id != "ce-ri-EC2-3" {
		t.Fatalf("fallback id = %q, want ce-ri-EC2-3", id)
	}

	// Non-string attribute values must not be formatted into the id.
	id = riRecommendationId("EC2", map[string]any{"region": 42}, 1)
	if strings.Contains(id, "42") {
		t.Fatalf("non-string attribute leaked into id: %q", id)
	}
}

func TestAllUpfrontBreakEvenMonths(t *testing.T) {
	// A 30% discount: committing $1/hr against on-demand that costs $1042.86/mo
	// for the same usage. 730 * 12 = $8760 upfront, recovered after ~8.4 months
	// of avoided on-demand spend — comfortably inside a 12 month term.
	got := allUpfrontBreakEvenMonths(1.0, 1042.86, 12)
	if got < 8.3 || got > 8.5 {
		t.Fatalf("break-even = %.2f months, want ~8.4", got)
	}
	if got > 12 {
		t.Fatalf("a 30%% discount was judged non-viable at %.2f months", got)
	}

	// The old formula divided by monthly savings (1042.86 - 730 = 312.86),
	// giving 28 months and suppressing this plan. Guard the regression.
	if old := (1.0 * 730 * 12) / 312.86; old <= 12 {
		t.Fatalf("test premise wrong: old formula gave %.2f months, expected > 12", old)
	}

	// A commitment that costs more per month than on-demand genuinely never pays
	// for itself, and the gate should still catch it.
	if got := allUpfrontBreakEvenMonths(2.0, 730.0, 12); got <= 12 {
		t.Fatalf("an above-on-demand commitment was judged viable at %.2f months", got)
	}

	// Unknown on-demand cost must not suppress the recommendation.
	if got := allUpfrontBreakEvenMonths(1.0, 0, 12); got != 0 {
		t.Fatalf("unknown on-demand cost gave %.2f, want 0", got)
	}
}
