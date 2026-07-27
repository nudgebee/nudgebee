package triage

import "testing"

// TestAssembleTiers covers the tiering rules directly (the replay harness scores
// them end-to-end against the labelled corpus; this pins each rule in the triage
// package so it stays covered independently).
func TestAssembleTiers(t *testing.T) {
	seed := AlertIdentity{ID: "seed", SubjectNamespace: "ns", SubjectOwner: "llm-server", AggregationKey: "ApplicationAPIFailures", TsOffsetS: 0}
	dependsOn := map[string][]string{
		"ns|llm-server":      {"ns|workflow-server"}, // llm-server calls workflow-server
		"ns|services-server": {"ns|llm-server"},      // services-server calls llm-server
	}
	rates := map[string]Rate{
		"ns|services-server|HighP95Latency": {Expected: 1.5, Observed: 2}, // chronic
		"ns|db-writer|HighP95Latency":       {Expected: 1.5, Observed: 9}, // bursting (9 > 3*1.5)
	}
	cands := []AlertIdentity{
		{ID: "same", SubjectNamespace: "ns", SubjectOwner: "llm-server", AggregationKey: "HighErrorCriticalLogs", TsOffsetS: -60},
		{ID: "same-hash", SubjectNamespace: "ns", SubjectName: "llm-server-6b9kfmn4p2", AggregationKey: "HighErrorCriticalLogs", TsOffsetS: 30},
		{ID: "seed-deploy", SubjectNamespace: "ns", SubjectOwner: "llm-server", IsConfigChange: true, TsOffsetS: -1800},
		{ID: "up-deploy", SubjectNamespace: "ns", SubjectOwner: "workflow-server", IsConfigChange: true, TsOffsetS: -1200},
		{ID: "up-alert", SubjectNamespace: "ns", SubjectOwner: "workflow-server", AggregationKey: "ApplicationAPIFailures", TsOffsetS: -120},
		{ID: "down-rare", SubjectNamespace: "ns", SubjectOwner: "services-server", AggregationKey: "OOMKilled", TsOffsetS: 90},
		{ID: "down-chronic", SubjectNamespace: "ns", SubjectOwner: "services-server", AggregationKey: "HighP95Latency", TsOffsetS: 90},
		{ID: "down-burst", SubjectNamespace: "ns", SubjectOwner: "db-writer", AggregationKey: "HighP95Latency", TsOffsetS: 90},
		{ID: "old-deploy", SubjectNamespace: "ns", SubjectOwner: "llm-server", IsConfigChange: true, TsOffsetS: -3 * 60 * 60}, // outside 2h lead-in
		{ID: "unrelated", SubjectNamespace: "ns", SubjectOwner: "billing", AggregationKey: "HighP95Latency", TsOffsetS: 30},
		// Derived signals: never a cause/impact, even though the topology + timing fit.
		{ID: "up-anomaly", SubjectNamespace: "ns", SubjectOwner: "workflow-server", AggregationKey: "Anomaly", FindingType: "Anomaly", TsOffsetS: -300},
		{ID: "down-slo", SubjectNamespace: "ns", SubjectOwner: "services-server", AggregationKey: "SLOViolation", FindingType: "SLO", TsOffsetS: 120},
		// ...but a derived signal on the seed's own subject is still core (context).
		{ID: "own-anomaly", SubjectNamespace: "ns", SubjectOwner: "llm-server", AggregationKey: "Anomaly", FindingType: "Anomaly", TsOffsetS: 45},
	}

	// db-writer is a dependent of services-server, so a dependent of the seed's chain
	// only via depth; make it a direct dependent for the burst case.
	dependsOn["ns|db-writer"] = []string{"ns|llm-server"}

	got := AssembleTiers(seed, cands, dependsOn, rates)

	want := map[string]string{
		"same":         TierCore,    // same subject
		"same-hash":    TierCore,    // hash-variant of same subject
		"seed-deploy":  TierCause,   // config change on seed, in lead-in
		"up-deploy":    TierCause,   // config change on upstream, in lead-in
		"up-alert":     TierCause,   // upstream alert before root
		"down-rare":    TierImpact,  // rare downstream alert after root
		"down-chronic": TierChronic, // chronic downstream -> folded
		"down-burst":   TierImpact,  // bursting past baseline -> stays impact
		"own-anomaly":  TierCore,    // derived signal on the seed's own subject is context
		// "old-deploy":  excluded (deploy older than the 2h lead-in)
		// "unrelated":   excluded (not seed / upstream / downstream)
		// "up-anomaly":  excluded (derived signal never becomes a cause)
		// "down-slo":    excluded (derived signal never becomes an impact)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("event %q: got tier %q, want %q", id, got[id], w)
		}
	}
	if _, ok := got["old-deploy"]; ok {
		t.Errorf("old-deploy should be excluded (outside 2h lead-in), got %q", got["old-deploy"])
	}
	if _, ok := got["unrelated"]; ok {
		t.Errorf("unrelated should be excluded, got %q", got["unrelated"])
	}
	// Derived signals (SLO / anomaly) must never be offered as cause or impact, even
	// though their subject and timing would otherwise qualify.
	for _, id := range []string{"up-anomaly", "down-slo"} {
		if tier, ok := got[id]; ok {
			t.Errorf("derived signal %q should be excluded from causal lanes, got %q", id, tier)
		}
	}
}

// WorkloadName is the single read-side rule for "which workload is this name". The
// knowledge graph reports a workload both bare and ReplicaSet-suffixed; both must reduce
// to the identity the event side produces, or a dependent can never be matched to its alert.
func TestWorkloadName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"product-reviews-76cf66f66b", "product-reviews"},
		{"load-generator-86b88dd659", "load-generator"},
		{"product-reviews", "product-reviews"},
		{"  Product-Reviews  ", "product-reviews"},
		// Real trailing words must survive — Kubernetes hashes use a vowel-free base32
		// alphabet, so anything containing a/e/i/o/u is a word, not a hash.
		{"llm-server", "llm-server"},
		{"cloud-collector-server", "cloud-collector-server"},
		{"postgresql-primary", "postgresql-primary"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := WorkloadName(tt.in); got != tt.want {
			t.Errorf("WorkloadName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
