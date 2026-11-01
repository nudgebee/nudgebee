package observability

import (
	"strings"
	"testing"
)

// The hand-written cases above prove the rewriter's rules. This one proves it against the
// expressions the utilisation layer ACTUALLY sends: every selector in every workload and node
// key an OpenObserve account can ask for must come back carrying the exclusion, and no
// grouping clause or duration may be touched.
func TestExcludeOpenObserveNoRecordedValueOnRealBuilderOutput(t *testing.T) {
	meta := RequestMetadata{
		Namespace: "nudgebee",
		Name:      "workflow-server",
		Kind:      "deployment",
		NodeName:  "gke-node-1",
	}

	// The keys this issue is about: the spec family that carried the marker values, plus
	// the usage, HTTP and PVC families that share the same builders.
	workloadKeys := []string{
		"cpu_request", "cpu_limit", "memory_request", "memory_limit",
		"container_cpu_request_by_pod", "container_cpu_limit_by_pod",
		"container_memory_request_by_pod", "container_memory_limit_by_pod",
		"container_cpu_usage_by_pod", "container_memory_working_set_by_pod",
		"container_oomkilled_limit_by_pod",
		"workload_http_request_rate", "workload_http_latency_mean",
		"pvc_used_by_claim", "pvc_capacity_by_claim",
		"replica_defined", "replica_ready",
	}
	nodeKeys := []string{"cpu_usage", "memory_usage", "disk_used", "disk_total", "cpu_request_pod", "cpu_limit_pod"}

	built := map[string]string{}
	for k, v := range buildPrometheusWorkloadQueries(meta, workloadKeys) {
		built[k] = v
	}
	for k, v := range buildPrometheusNodeQueries(meta, nodeKeys) {
		built["node/"+k] = v
	}
	if len(built) == 0 {
		t.Fatal("no queries built -- the key names or builder signature changed")
	}

	for key, expr := range built {
		got := excludeOpenObserveNoRecordedValue(expr)

		// Every selector gained the exclusion: one per `{`, and the count of `{` must not
		// have changed (a new brace would mean a selector was invented).
		wantSelectors := strings.Count(got, "{")
		gotMatchers := strings.Count(got, openObserveNoRecordedValueMatcher)
		if gotMatchers != wantSelectors {
			t.Errorf("%s: %d selector(s) but %d exclusion(s)\n  in  %s\n  got %s",
				key, wantSelectors, gotMatchers, expr, got)
		}

		// Grouping clauses and durations must be untouched, which shows up as a selector
		// appearing where a label list or a range belongs.
		for _, bad := range []string{"by (", "on (", "group_left(", "group_right("} {
			idx := 0
			for {
				at := strings.Index(got[idx:], bad)
				if at == -1 {
					break
				}
				at += idx
				end := strings.Index(got[at:], ")")
				if end != -1 && strings.Contains(got[at:at+end], "{") {
					t.Errorf("%s: selector injected into a %s clause\n  got %s", key, bad, got)
				}
				idx = at + len(bad)
			}
		}
		if strings.Contains(got, "{"+openObserveNoRecordedValueMatcher+"}m]") ||
			strings.Contains(got, "{"+openObserveNoRecordedValueMatcher+"}s]") {
			t.Errorf("%s: selector injected into a duration\n  got %s", key, got)
		}

		// And the rewrite must be stable.
		if again := excludeOpenObserveNoRecordedValue(got); again != got {
			t.Errorf("%s: not idempotent\n  once  %s\n  twice %s", key, got, again)
		}
	}
	t.Logf("checked %d real builder expressions", len(built))
}
