package anomoly

import (
	"fmt"
	"testing"
)

// TestAnomalyFingerprint_Format pins down the exact string shape so a future
// edit to anomalyFingerprint can't silently drift from what's documented in
// CLAUDE.md and what any external consumer (dashboards, support queries
// against events.finding_id) might rely on.
func TestAnomalyFingerprint_Format(t *testing.T) {
	got := anomalyFingerprint("acct-1", MetricAnomolyTypeCPU, "api", "prod")
	want := "anomaly|acct-1|CPU|api|prod"
	if got != want {
		t.Fatalf("anomalyFingerprint() = %q, want %q", got, want)
	}
}

// TestAnomalyFingerprint_StableAcrossCalls is the property GenerateAnomalyEvent
// and closeAnomalyEventIfOpen both depend on: given the same identity, the
// function must return byte-for-byte the same string every time, regardless
// of call order or how many times it's invoked. This is what lets
// InsertEvent's ON CONFLICT(tenant, cloud_account_id, finding_id) upsert
// collapse repeat detections into one row instead of inserting a new row
// every cycle, and what lets closeAnomalyEventIfOpen's point lookup find the
// row GenerateAnomalyEvent created.
func TestAnomalyFingerprint_StableAcrossCalls(t *testing.T) {
	const n = 5
	first := anomalyFingerprint("11111111-1111-1111-1111-111111111111", MetricAnomolyTypeMemory, "checkout", "prod-payments")
	for i := range n {
		got := anomalyFingerprint("11111111-1111-1111-1111-111111111111", MetricAnomolyTypeMemory, "checkout", "prod-payments")
		if got != first {
			t.Fatalf("call %d: anomalyFingerprint() = %q, want %q (same as first call)", i, got, first)
		}
	}
}

// TestAnomalyFingerprint_DistinctIdentitiesProduceDistinctFingerprints is the
// converse property: any change to account, type, name, or namespace alone
// must change the fingerprint. If two logically different anomaly instances
// ever produced the same fingerprint, closeAnomalyEventIfOpen's point lookup
// (keyed only on tenant+account+finding_id) would close the wrong one, or
// GenerateAnomalyEvent would silently merge two unrelated anomalies into one
// event row.
func TestAnomalyFingerprint_DistinctIdentitiesProduceDistinctFingerprints(t *testing.T) {
	base := struct {
		account, name, namespace string
		anomalyType              AnomalyType
	}{"acct-1", "api", "prod", MetricAnomolyTypeCPU}

	variants := map[string]string{
		"different account":   anomalyFingerprint("acct-2", base.anomalyType, base.name, base.namespace),
		"different type":      anomalyFingerprint(base.account, MetricAnomolyTypeMemory, base.name, base.namespace),
		"different name":      anomalyFingerprint(base.account, base.anomalyType, "worker", base.namespace),
		"different namespace": anomalyFingerprint(base.account, base.anomalyType, base.name, "staging"),
	}

	baseline := anomalyFingerprint(base.account, base.anomalyType, base.name, base.namespace)
	seen := map[string]string{"baseline": baseline}
	for label, fp := range variants {
		if fp == baseline {
			t.Errorf("%s: fingerprint %q collided with the baseline identity's fingerprint", label, fp)
		}
		for otherLabel, otherFP := range seen {
			if fp == otherFP {
				t.Errorf("%s and %s produced the same fingerprint %q for different identities", label, otherLabel, fp)
			}
		}
		seen[label] = fp
	}
}

// TestAnomalyFingerprint_NoHyphenSplitAmbiguity is the regression test for the
// actual bug this format fixes. Kubernetes names and namespaces are DNS-1123
// labels that routinely contain "-", and cloud_account_id is a UUID (also
// full of "-"). A "-"-joined fingerprint is ambiguous: name="api",
// namespace="prod-payments" and name="api-prod", namespace="payments" both
// serialize to "anomaly-<acct>-CPU-api-prod-payments" under a naive
// fmt.Sprintf("anomaly-%s-%s-%s-%s", ...) — two unrelated workloads would
// silently share one event and incorrectly close each other's anomalies.
// anomalyFingerprint uses "|" specifically to rule this out.
func TestAnomalyFingerprint_NoHyphenSplitAmbiguity(t *testing.T) {
	const account = "22222222-2222-2222-2222-222222222222"

	cases := []struct {
		name      string
		namespace string
	}{
		{"api", "prod-payments"},
		{"api-prod", "payments"},
	}

	// Sanity check the premise: these two identities really do collide under
	// the old "-"-joined format, so this test is proving something real.
	oldStyle := func(name, namespace string) string {
		return fmt.Sprintf("anomaly-%s-%s-%s-%s", account, MetricAnomolyTypeCPU, name, namespace)
	}
	if oldStyle(cases[0].name, cases[0].namespace) != oldStyle(cases[1].name, cases[1].namespace) {
		t.Fatalf("test premise invalid: the two identities no longer collide under a naive \"-\"-joined format")
	}

	fp0 := anomalyFingerprint(account, MetricAnomolyTypeCPU, cases[0].name, cases[0].namespace)
	fp1 := anomalyFingerprint(account, MetricAnomolyTypeCPU, cases[1].name, cases[1].namespace)
	if fp0 == fp1 {
		t.Fatalf("anomalyFingerprint collided for distinct identities %+v and %+v: both produced %q", cases[0], cases[1], fp0)
	}
}

// TestAnomalyFingerprint_NoPipeCharacterInInputs documents why "|" is a safe
// delimiter: none of the four fields can legally contain it. A UUID is hex
// digits and "-"; this type's enum values are plain ASCII words; Kubernetes
// names/namespaces are DNS-1123 labels ([a-z0-9-]). If any of those ever
// gained the ability to contain "|", this delimiter choice would need
// revisiting — this test exists so that assumption is written down somewhere
// executable, not just in a comment.
func TestAnomalyFingerprint_NoPipeCharacterInInputs(t *testing.T) {
	for _, at := range []AnomalyType{
		MetricAnomolyTypeLatency, MetricAnomolyTypeMemory, MetricAnomolyTypeCPU,
		MetricAnomolyTypeNetwork, MetricAnomolyTypeErrorRate, MetricAnomolyTypeReplicas,
		MetricAnomolyTypeCloudSpendAccount, MetricAnomolyTypeCloudSpendService,
	} {
		if containsPipe(string(at)) {
			t.Errorf("AnomalyType %q contains \"|\", which would break anomalyFingerprint's delimiter assumption", at)
		}
	}
}

func containsPipe(s string) bool {
	for _, r := range s {
		if r == '|' {
			return true
		}
	}
	return false
}
