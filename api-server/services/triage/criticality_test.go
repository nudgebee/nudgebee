package triage

import "testing"

func TestDeriveCriticalityFromFacts(t *testing.T) {
	cases := []struct {
		name      string
		facts     workloadFacts
		wantLevel string
		wantOK    bool
	}{
		{"no workload", workloadFacts{found: false}, "", false},
		{"no signal", workloadFacts{found: true, graphKnown: true, fanIn: 1}, "", false},
		{"customer-facing => high (critical is human-only)", workloadFacts{found: true, graphKnown: true, customerFacing: true}, CriticalityHigh, true},
		{"high fan-in => high", workloadFacts{found: true, graphKnown: true, fanIn: 15}, CriticalityHigh, true},
		{"fan-in below threshold => none", workloadFacts{found: true, graphKnown: true, fanIn: 5}, "", false},
		{"declared critical label still wins", workloadFacts{found: true, graphKnown: true, fanIn: 15, declaredTier: "tier=critical"}, CriticalityCritical, true},
		{"customer-facing needs graph", workloadFacts{found: true, graphKnown: false, customerFacing: true}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lvl, _, _, ok := deriveCriticalityFromFacts(c.facts)
			if ok != c.wantOK || lvl != c.wantLevel {
				t.Fatalf("got (%q, ok=%v), want (%q, ok=%v)", lvl, ok, c.wantLevel, c.wantOK)
			}
		})
	}
}

func TestNormalizeDeclaredTier(t *testing.T) {
	cases := map[string]string{
		"tier=critical":    CriticalityCritical,
		"criticality=Gold": CriticalityCritical,
		"tier=tier-1":      CriticalityHigh,
		"tier=P2":          CriticalityMedium,
		"service.tier=low": CriticalityLow,
		"tier=42":          "", // numeric-only is too ambiguous
		"random=value":     "",
	}
	for in, want := range cases {
		if got := normalizeDeclaredTier(in); got != want {
			t.Errorf("normalizeDeclaredTier(%q) = %q, want %q", in, got, want)
		}
	}
}
