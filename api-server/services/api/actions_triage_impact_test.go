package api

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/triage"
)

// The knowledge graph holds the same workload under two names — the bare one and the
// ReplicaSet-suffixed one. Both were observed in a single blast-radius response for the
// demo/flagd incident. The suffixed copy can never match an alert (events carry the bare
// owning workload in subject_owner), so before this collapse it showed up as a second,
// permanently non-alerting dependent.
func TestScopeAndNormalizeCollapsesReplicaSetDuplicates(t *testing.T) {
	in := []core.ImpactedService{
		{Name: "load-generator", Namespace: "demo", HopsAway: 2},
		{Name: "load-generator-86b88dd659", Namespace: "demo", HopsAway: 1},
		{Name: "product-reviews-76cf66f66b", Namespace: "demo", HopsAway: 1},
		{Name: "product-reviews", Namespace: "demo", HopsAway: 1},
		{Name: "ad", Namespace: "demo", HopsAway: 1},
		// Cross-namespace name collision (#34569) — scoped away entirely.
		{Name: "product-reviews", Namespace: "other", HopsAway: 1},
	}

	got := scopeAndNormalize(in, "demo")

	if len(got) != 3 {
		t.Fatalf("expected 3 distinct workloads, got %d: %+v", len(got), got)
	}
	byName := map[string]core.ImpactedService{}
	for _, s := range got {
		if _, dup := byName[s.Name]; dup {
			t.Fatalf("duplicate workload %q survived: %+v", s.Name, got)
		}
		byName[s.Name] = s
	}
	for _, want := range []string{"load-generator", "product-reviews", "ad"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("missing %q in %+v", want, got)
		}
	}
	// The closest path is the one worth reporting: the 1-hop suffixed copy must win over
	// the 2-hop bare one, not simply be dropped as "seen already".
	if h := byName["load-generator"].HopsAway; h != 1 {
		t.Fatalf("hops_away = %d, want the shallower 1", h)
	}
}

// The whole point of the collapse is that the surviving name keys the topology under the
// identity an alert on that service actually produces. This is the assertion that would
// have caught the bug: a downstream alert could not reach the impact tier because the
// graph's key carried a hash and the event's key did not.
func TestNormalizedDependentKeyMatchesAlertIdentity(t *testing.T) {
	deps := scopeAndNormalize([]core.ImpactedService{
		{Name: "product-reviews-76cf66f66b", Namespace: "demo", HopsAway: 1},
	}, "demo")
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependent, got %+v", deps)
	}

	topoKey := triage.SubjectKey(triage.AlertIdentity{SubjectNamespace: deps[0].Namespace, SubjectName: deps[0].Name})
	// What an alert on that service looks like coming out of the events table.
	alertKey := triage.SubjectKey(triage.AlertIdentity{
		SubjectNamespace: "demo",
		SubjectName:      "product-reviews-76cf66f66b-x8k2p",
		SubjectOwner:     "product-reviews",
	})
	if topoKey != alertKey {
		t.Fatalf("dependent key %q does not match alert key %q — the alert cannot reach the impact tier", topoKey, alertKey)
	}
}

func TestScopeAndNormalizeKeepsEverythingWhenSeedHasNoNamespace(t *testing.T) {
	in := []core.ImpactedService{
		{Name: "svc-a", Namespace: "one", HopsAway: 1},
		{Name: "svc-b", Namespace: "two", HopsAway: 1},
	}
	if got := scopeAndNormalize(in, ""); len(got) != 2 {
		t.Fatalf("empty namespace must not scope anything away, got %+v", got)
	}
}

func TestScopeAndNormalizeDropsEmptyNames(t *testing.T) {
	in := []core.ImpactedService{
		{Name: "", Namespace: "demo", HopsAway: 1},
		{Name: "   ", Namespace: "demo", HopsAway: 1},
		{Name: "ad", Namespace: "demo", HopsAway: 1},
	}
	got := scopeAndNormalize(in, "demo")
	if len(got) != 1 || got[0].Name != "ad" {
		t.Fatalf("expected only the named dependent, got %+v", got)
	}
}
