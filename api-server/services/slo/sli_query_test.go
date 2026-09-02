package slo

import (
	"regexp"
	"strings"
	"testing"
)

// Prometheus anchors label regexes fully, so match the same way here.
func failedStatusMatcher(t *testing.T) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile("^(?:" + FailedRequestStatusRe + ")$")
	if err != nil {
		t.Fatalf("FailedRequestStatusRe does not compile: %v", err)
	}
	return re
}

// Mirrors coroot's model.IsRequestStatusFailed: 5xx, "failed", non-OK gRPC.
func TestFailedRequestStatusRe(t *testing.T) {
	re := failedStatusMatcher(t)
	for _, tc := range []struct {
		status string
		failed bool
	}{
		{"500", true},
		{"503", true},
		{"599", true},
		{"failed", true},
		{"grpc:NotFound", true},
		{"grpc:Unavailable", true},
		{"grpc:OutOfRange", true}, // starts with O but is not OK
		{"grpc:OK", false},
		{"200", false},
		{"204", false},
		{"301", false}, // 3xx used to be counted in neither good nor bad
		{"404", false}, // client error is not a server availability failure
		{"429", false},
	} {
		if got := re.MatchString(tc.status); got != tc.failed {
			t.Errorf("status %q: failed = %v; want %v", tc.status, got, tc.failed)
		}
	}
}

// good and bad must partition every request, so good+bad is the true total and
// the SLI works out to 1 - failed/total. The old pair matched 2.. and 5.. and
// therefore dropped 3xx and 4xx from the measurement entirely.
func TestAvailabilityQueriesPartitionAllRequests(t *testing.T) {
	good := AvailabilityGoodQuery("shop", "web")
	bad := AvailabilityBadQuery("shop", "web")

	if !strings.Contains(good, `status!~"`+FailedRequestStatusRe+`"`) {
		t.Errorf("good query does not negate the failed statuses: %s", good)
	}
	if !strings.Contains(bad, `status=~"`+FailedRequestStatusRe+`"`) {
		t.Errorf("bad query does not match the failed statuses: %s", bad)
	}
	// Same series, same workload selectors — only the status matcher differs.
	if strings.Replace(good, `status!~`, `status=~`, 1) != bad {
		t.Errorf("good and bad are not complements:\n good=%s\n bad =%s", good, bad)
	}
	for _, q := range []string{good, bad} {
		if !strings.Contains(q, `destination_workload_namespace="shop"`) || !strings.Contains(q, `destination_workload_name="web"`) {
			t.Errorf("workload selectors missing: %s", q)
		}
		if strings.Contains(q, `status=~"2.."`) {
			t.Errorf("query still uses the 2xx-only numerator: %s", q)
		}
	}
}

func TestLatencyHistogramQueryDropsDeadExternalMatcher(t *testing.T) {
	q := LatencyHistogramQuery("shop", "web")
	if strings.Contains(q, "external") {
		t.Errorf("dead namespace!=\"external\" matcher still present: %s", q)
	}
	if !strings.Contains(q, `destination_workload_namespace="shop"`) || !strings.Contains(q, `destination_workload_name="web"`) {
		t.Errorf("workload selectors missing: %s", q)
	}
}

// threshold is the objective bucket in milliseconds and trace Duration is in
// nanoseconds. Converting to seconds compared seconds against milliseconds, so
// a 500ms objective only ever flagged traces slower than 500 seconds.
func TestFilterLongTracesByWorkload_ThresholdIsMilliseconds(t *testing.T) {
	const ms = 1e6
	traces := []map[string]any{
		{"workload_name": "web", "workload_namespace": "shop", "Duration": 600 * ms},
		{"workload_name": "web", "workload_namespace": "shop", "Duration": 400 * ms},
		{"workload_name": "other", "workload_namespace": "shop", "Duration": 900 * ms},
		{"workload_name": "web", "workload_namespace": "elsewhere", "Duration": 900 * ms},
	}

	got := FilterLongTracesByWorkload(traces, 500, "web", "shop")
	if len(got) != 1 {
		t.Fatalf("matched %d traces; want 1 (the 600ms one): %+v", len(got), got)
	}
	if got[0]["Duration"] != 600*ms {
		t.Errorf("matched the wrong trace: %+v", got[0])
	}
}

// otel_traces.Duration is a ClickHouse Int64 and the relay serves rows as
// `FORMAT JSON`, which quotes 64-bit integers by default — so on the
// ClickHouse-backed path the cell arrives as a string. The old strict
// float64 assertion dropped every such row, which would have kept the
// insight silent even with the millisecond conversion fixed.
func TestFilterLongTracesByWorkload_AcceptsQuotedInt64Duration(t *testing.T) {
	traces := []map[string]any{
		{"workload_name": "web", "workload_namespace": "shop", "Duration": "600000000"},
		{"workload_name": "web", "workload_namespace": "shop", "Duration": "400000000"},
		{"workload_name": "web", "workload_namespace": "shop", "Duration": "not-a-number"},
		{"workload_name": "web", "workload_namespace": "shop"},
	}

	got := FilterLongTracesByWorkload(traces, 500, "web", "shop")
	if len(got) != 1 {
		t.Fatalf("matched %d traces; want 1 (the 600ms one): %+v", len(got), got)
	}
	if got[0]["Duration"] != "600000000" {
		t.Errorf("matched the wrong trace: %+v", got[0])
	}
}
