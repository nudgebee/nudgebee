package core

import (
	"strings"
	"testing"
)

func TestCanonicalFingerprint(t *testing.T) {
	// Deterministic: the same alert identity always yields the same fingerprint,
	// so repeat firings (which differ only in per-delivery ids the helper never
	// sees) collapse into one occurrence chain.
	a := CanonicalFingerprint("pagerduty", "HighErrorRate", "nb-bench", "app")
	if a != CanonicalFingerprint("pagerduty", "HighErrorRate", "nb-bench", "app") {
		t.Fatal("expected deterministic fingerprint for identical identity")
	}

	// Different alert-type must not collapse (guards against over-merge).
	if a == CanonicalFingerprint("pagerduty", "HighLatency", "nb-bench", "app") {
		t.Error("different alert-type must produce a different fingerprint")
	}

	// Different subject must not collapse.
	if a == CanonicalFingerprint("pagerduty", "HighErrorRate", "nb-bench", "cart") {
		t.Error("different subject must produce a different fingerprint")
	}

	// Different namespace must not collapse.
	if a == CanonicalFingerprint("pagerduty", "HighErrorRate", "prod", "app") {
		t.Error("different namespace must produce a different fingerprint")
	}

	// Different source must not collapse (Step 1 keeps sources distinct; cross-
	// source dedup is a separate, later step).
	if a == CanonicalFingerprint("splunk", "HighErrorRate", "nb-bench", "app") {
		t.Error("different source must produce a different fingerprint")
	}

	// The NUL separator prevents part-boundary collisions: ("ab","c") vs ("a","bc").
	if CanonicalFingerprint("ab", "c") == CanonicalFingerprint("a", "bc") {
		t.Error("separator must prevent part-boundary collisions")
	}

	// Marked with the canonical prefix so canonicalized rows are recognizable.
	if !strings.HasPrefix(a, canonicalFingerprintPrefix) {
		t.Errorf("expected %q prefix, got %q", canonicalFingerprintPrefix, a)
	}
}
