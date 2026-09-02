package triage

import "testing"

// ingressBacked is the candidate shape at the heart of the bug this file guards: a workload the
// deterministic pass measured as ingress/LB-backed and tiered `high` at 0.9 confidence.
func ingressBacked() candidateRow {
	return candidateRow{
		crid:     "11111111-1111-1111-1111-111111111111",
		detLevel: CriticalityHigh,
		detRat:   "ingress/LB-backed (customer-facing request path)",
		detConf:  0.9,
	}
}

func TestResolveCandidateVerdict(t *testing.T) {
	cases := []struct {
		name        string
		verdict     llmCriticalityVerdict
		hasVerdict  bool
		wantLevel   string
		wantSource  string
		wantOutcome string
	}{
		{
			// The regression: a `medium` answer used to erase the measured tier, leaving genuinely
			// ingress-backed workloads (app-dev, ingress-nginx-controller) on the medium default.
			name:        "medium is no opinion and must not erase the measured tier",
			verdict:     llmCriticalityVerdict{Criticality: CriticalityMedium, Reason: "standard service"},
			hasVerdict:  true,
			wantLevel:   CriticalityHigh,
			wantSource:  CriticalitySourceFact,
			wantOutcome: outcomeNoOpinion,
		},
		{
			name:        "low is an active demotion and does replace it",
			verdict:     llmCriticalityVerdict{Criticality: CriticalityLow, Reason: "docs site"},
			hasVerdict:  true,
			wantLevel:   CriticalityLow,
			wantSource:  CriticalitySourceLLM,
			wantOutcome: outcomeDemoted,
		},
		{
			name:        "critical promotes",
			verdict:     llmCriticalityVerdict{Criticality: CriticalityCritical, Reason: "auth provider"},
			hasVerdict:  true,
			wantLevel:   CriticalityCritical,
			wantSource:  CriticalitySourceLLM,
			wantOutcome: outcomeConfirmed,
		},
		{
			name:        "high confirms",
			verdict:     llmCriticalityVerdict{Criticality: CriticalityHigh, Reason: "shared gateway"},
			hasVerdict:  true,
			wantLevel:   CriticalityHigh,
			wantSource:  CriticalitySourceLLM,
			wantOutcome: outcomeConfirmed,
		},
		{
			name:        "no verdict at all keeps the deterministic tier",
			hasVerdict:  false,
			wantLevel:   CriticalityHigh,
			wantSource:  CriticalitySourceFact,
			wantOutcome: outcomeKept,
		},
		{
			name:        "an unparseable tier is ignored rather than applied",
			verdict:     llmCriticalityVerdict{Criticality: "URGENT", Reason: "junk"},
			hasVerdict:  true,
			wantLevel:   CriticalityHigh,
			wantSource:  CriticalitySourceFact,
			wantOutcome: outcomeKept,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveCandidateVerdict(ingressBacked(), c.verdict, c.hasVerdict)
			if got.level != c.wantLevel {
				t.Errorf("level = %q, want %q", got.level, c.wantLevel)
			}
			if got.source != c.wantSource {
				t.Errorf("source = %q, want %q", got.source, c.wantSource)
			}
			if got.outcome != c.wantOutcome {
				t.Errorf("outcome = %q, want %q", got.outcome, c.wantOutcome)
			}
		})
	}
}

// A no-opinion answer must keep the deterministic RATIONALE and CONFIDENCE too, not just the level —
// otherwise the review screen shows an LLM justification for a fact-derived tier.
func TestResolveCandidateVerdictNoOpinionKeepsProvenance(t *testing.T) {
	c := ingressBacked()
	got := resolveCandidateVerdict(c, llmCriticalityVerdict{Criticality: CriticalityMedium, Reason: "unsure"}, true)
	if got.rationale != c.detRat {
		t.Errorf("rationale = %q, want the deterministic %q", got.rationale, c.detRat)
	}
	if got.confidence != c.detConf {
		t.Errorf("confidence = %v, want the deterministic %v", got.confidence, c.detConf)
	}
}

// A deterministic verdict that is itself medium (an operator label of tier=medium) still yields no
// row — medium is the unstored default regardless of who decided it.
func TestResolveCandidateVerdictDeterministicMediumStaysMedium(t *testing.T) {
	c := candidateRow{crid: "x", detLevel: CriticalityMedium, detRat: "operator-declared label tier=medium", detConf: 0.95}
	if got := resolveCandidateVerdict(c, llmCriticalityVerdict{}, false); got.level != CriticalityMedium {
		t.Fatalf("level = %q, want %q", got.level, CriticalityMedium)
	}
}
