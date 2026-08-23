package replay

import (
	"testing"
)

// TestReplayHarness is the correlation-quality gate (#34660). It scores the
// reference groupers against the labelled audit corpus and asserts that today's
// behaviour fragments real incidents (the audit baseline) while the intended
// subject normalization recovers them without over-merging.
func TestReplayHarness(t *testing.T) {
	set, err := LoadGoldenSet("testdata/golden_audit.json")
	if err != nil {
		t.Fatalf("load golden set: %v", err)
	}
	if len(set.Events) == 0 {
		t.Fatal("golden set is empty")
	}

	baseline := Score(set, BaselineGrouper{})
	normalized := Score(set, NormalizedGrouper{})

	for _, r := range []ScoreResult{baseline, normalized} {
		t.Logf(
			"%-11s precision=%.2f recall=%.2f f1=%.2f  groups=%d  (tp=%d fp=%d fn=%d)",
			r.Grouper, r.Precision, r.Recall, r.F1, r.Groups, r.TP, r.FP, r.FN)
	}

	// Baseline reproduces the audit: raw subject names fragment same-incident
	// alerts (ReplicaSet-hash variants and per-datname postgres alerts), so recall
	// is low even though it never wrongly merges (precision stays high).
	if baseline.Recall >= 0.6 {
		t.Errorf("expected baseline to fragment incidents (recall < 0.60), got %.2f", baseline.Recall)
	}
	if baseline.Precision < 0.99 {
		t.Errorf("expected baseline precision >= 0.99 (it only splits, never merges), got %.2f", baseline.Precision)
	}

	// Normalization recovers the fragmented groups (owner / hash-strip / shared
	// datastore signal) without merging unrelated incidents.
	if normalized.Recall < 0.95 {
		t.Errorf("expected normalized recall >= 0.95, got %.2f", normalized.Recall)
	}
	if normalized.Precision < 0.99 {
		t.Errorf("expected normalized precision >= 0.99 (no over-merge), got %.2f", normalized.Precision)
	}

	// The whole point: normalization must strictly improve recall over baseline.
	if normalized.Recall <= baseline.Recall {
		t.Errorf("normalization did not improve recall: baseline=%.2f normalized=%.2f",
			baseline.Recall, normalized.Recall)
	}
}
