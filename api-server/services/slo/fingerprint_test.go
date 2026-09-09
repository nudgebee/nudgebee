package slo

import (
	"fmt"
	"testing"
)

// TestSLOFingerprint_Format pins down the exact string shape so a future edit
// to sloFingerprint can't silently drift.
func TestSLOFingerprint_Format(t *testing.T) {
	got := sloFingerprint("acct-1", "latency", "api", "prod")
	want := "slo|acct-1|latency|api|prod"
	if got != want {
		t.Fatalf("sloFingerprint() = %q, want %q", got, want)
	}
}

// TestSLOFingerprint_StableAcrossCalls is the property GenerateSLOEvent,
// closeSLOEventIfRecovered, DeleteSLOConfig, and CreateOrUpdateSLOConfig all
// depend on: the same identity must always produce the same string, which is
// what lets InsertEvent's upsert collapse repeat violations into one row and
// what lets the close paths' point lookups find the row GenerateSLOEvent
// created.
func TestSLOFingerprint_StableAcrossCalls(t *testing.T) {
	const n = 5
	first := sloFingerprint("11111111-1111-1111-1111-111111111111", "availability", "checkout", "prod-payments")
	for i := range n {
		got := sloFingerprint("11111111-1111-1111-1111-111111111111", "availability", "checkout", "prod-payments")
		if got != first {
			t.Fatalf("call %d: sloFingerprint() = %q, want %q (same as first call)", i, got, first)
		}
	}
}

// TestSLOFingerprint_DistinctIdentitiesProduceDistinctFingerprints: any change
// to account, config name, workload, or namespace alone must change the
// fingerprint, or closeSLOEventByFingerprint's point lookup (keyed only on
// tenant+account+finding_id) could resolve the wrong workload's violation.
func TestSLOFingerprint_DistinctIdentitiesProduceDistinctFingerprints(t *testing.T) {
	base := struct{ account, configName, workload, namespace string }{"acct-1", "latency", "api", "prod"}

	variants := map[string]string{
		"different account":     sloFingerprint("acct-2", base.configName, base.workload, base.namespace),
		"different config name": sloFingerprint(base.account, "availability", base.workload, base.namespace),
		"different workload":    sloFingerprint(base.account, base.configName, "worker", base.namespace),
		"different namespace":   sloFingerprint(base.account, base.configName, base.workload, "staging"),
	}

	baseline := sloFingerprint(base.account, base.configName, base.workload, base.namespace)
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

// TestSLOFingerprint_NoHyphenSplitAmbiguity is the regression test for the
// actual bug this format fixes. Kubernetes workload/namespace names are
// DNS-1123 labels that routinely contain "-", and cloud_account_id is a UUID.
// A "-"-joined fingerprint is ambiguous: workload="api",
// namespace="prod-payments" and workload="api-prod", namespace="payments"
// both serialize to the identical string under a naive
// fmt.Sprintf("slo-%s-%s-%s-%s", ...) for the same account+config name — two
// unrelated workloads would silently share one event and incorrectly close
// each other's violations. sloFingerprint uses "|" specifically to rule this
// out.
func TestSLOFingerprint_NoHyphenSplitAmbiguity(t *testing.T) {
	const account = "22222222-2222-2222-2222-222222222222"
	const configName = "latency"

	cases := []struct{ workload, namespace string }{
		{"api", "prod-payments"},
		{"api-prod", "payments"},
	}

	oldStyle := func(workload, namespace string) string {
		return fmt.Sprintf("slo-%s-%s-%s-%s", account, configName, workload, namespace)
	}
	if oldStyle(cases[0].workload, cases[0].namespace) != oldStyle(cases[1].workload, cases[1].namespace) {
		t.Fatalf("test premise invalid: the two identities no longer collide under a naive \"-\"-joined format")
	}

	fp0 := sloFingerprint(account, configName, cases[0].workload, cases[0].namespace)
	fp1 := sloFingerprint(account, configName, cases[1].workload, cases[1].namespace)
	if fp0 == fp1 {
		t.Fatalf("sloFingerprint collided for distinct identities %+v and %+v: both produced %q", cases[0], cases[1], fp0)
	}
}

// TestSLOFingerprint_ConfigNamesHaveNoPipeCharacter documents why "|" is a
// safe delimiter for the config-name field: both values it's created with
// today ("latency", "availability" — see CreateOrUpdateSLOConfig's name
// switch) are plain lowercase words. Written down as an executable
// assumption rather than only a comment.
func TestSLOFingerprint_ConfigNamesHaveNoPipeCharacter(t *testing.T) {
	for _, name := range []string{"latency", "availability"} {
		for _, r := range name {
			if r == '|' {
				t.Errorf("SLO config name %q contains \"|\", which would break sloFingerprint's delimiter assumption", name)
			}
		}
	}
}
