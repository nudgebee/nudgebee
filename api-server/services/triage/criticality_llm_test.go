package triage

import (
	"context"
	"strings"
	"testing"
)

func batchOfThree() []llmWorkload {
	return []llmWorkload{
		{CloudResourceID: "crid-1", Name: "checkout", Namespace: "shop", Kind: "Deployment"},
		{CloudResourceID: "crid-2", Name: "docs", Namespace: "shop", Kind: "Deployment"},
		{CloudResourceID: "crid-3", Name: "postgres", Namespace: "shop", Kind: "StatefulSet"},
	}
}

func TestParseCriticalityVerdicts(t *testing.T) {
	cases := []struct {
		name string
		text string
		want map[string]string // cloud_resource_id -> criticality
	}{
		{
			name: "well-formed response maps by index",
			text: `[{"i":1,"name":"checkout","criticality":"critical","reason":"payments"},
			        {"i":2,"name":"docs","criticality":"low","reason":"documentation"}]`,
			want: map[string]string{"crid-1": CriticalityCritical, "crid-2": CriticalityLow},
		},
		{
			name: "markdown fence and prose around the array are tolerated",
			text: "Here you go:\n```json\n[{\"i\":3,\"name\":\"postgres\",\"criticality\":\"high\",\"reason\":\"shared db\"}]\n```",
			want: map[string]string{"crid-3": CriticalityHigh},
		},
		{
			// The dangerous failure: a name that does not belong to that index means the model's
			// numbering drifted, and applying it would tier the WRONG workload.
			name: "an entry whose name does not match its index is dropped",
			text: `[{"i":1,"name":"postgres","criticality":"critical","reason":"drifted"}]`,
			want: map[string]string{},
		},
		{
			name: "a name omitted entirely is still accepted",
			text: `[{"i":2,"criticality":"low","reason":"documentation"}]`,
			want: map[string]string{"crid-2": CriticalityLow},
		},
		{
			// k8s names are lowercase by RFC 1123, so differing case can only have come from the
			// model's own formatting — never from two distinct workloads colliding.
			name: "a name differing only by case or surrounding space still matches",
			text: `[{"i":1,"name":"  Checkout ","criticality":"critical","reason":"payments"}]`,
			want: map[string]string{"crid-1": CriticalityCritical},
		},
		{
			// Continuing the numbering across batches is a common model behaviour; every entry is
			// out of range and must be dropped rather than wrapped around onto real workloads.
			name: "indexes past the end of the batch are dropped",
			text: `[{"i":41,"name":"checkout","criticality":"critical","reason":"cross-batch numbering"}]`,
			want: map[string]string{},
		},
		{
			name: "an index of zero is dropped",
			text: `[{"i":0,"name":"checkout","criticality":"critical","reason":"one-off-by-one"}]`,
			want: map[string]string{},
		},
		{
			name: "an unrecognised tier is dropped, the rest survive",
			text: `[{"i":1,"name":"checkout","criticality":"URGENT","reason":"junk"},
			        {"i":2,"name":"docs","criticality":"low","reason":"documentation"}]`,
			want: map[string]string{"crid-2": CriticalityLow},
		},
		{
			name: "unparseable output yields nothing rather than panicking",
			text: `I could not classify these workloads.`,
			want: map[string]string{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := map[string]llmCriticalityVerdict{}
			parseCriticalityVerdicts(context.Background(), c.text, batchOfThree(), out)
			if len(out) != len(c.want) {
				t.Fatalf("got %d verdicts %v, want %d %v", len(out), out, len(c.want), c.want)
			}
			for crid, level := range c.want {
				if got, ok := out[crid]; !ok || got.Criticality != level {
					t.Errorf("%s: got %+v, want criticality %q", crid, got, level)
				}
			}
		})
	}
}

// The prompt has to be unambiguous about the two things that broke: what `medium` means, and that the
// name is echoed back for verification. These assertions are deliberately about intent, not wording.
func TestCriticalityClassifyPromptStatesMediumIsNoOpinion(t *testing.T) {
	p := criticalityClassifyPrompt(batchOfThree())

	if strings.Contains(p, "use it when unsure") {
		t.Error("prompt still tells the model to default to medium when unsure; medium no longer demotes")
	}
	if !strings.Contains(p, "no opinion") {
		t.Error("prompt must state that medium means no opinion")
	}
	if !strings.Contains(p, `"name"`) {
		t.Error("prompt must ask for the workload name to be echoed back")
	}
	for _, w := range batchOfThree() {
		if !strings.Contains(p, w.Name) {
			t.Errorf("prompt is missing workload %q", w.Name)
		}
	}
}
