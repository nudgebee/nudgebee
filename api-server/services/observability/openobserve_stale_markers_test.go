package observability

import "testing"

const nrv = `flag!="DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK"`

func TestExcludeOpenObserveNoRecordedValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare metric name",
			in:   `container_cpu_usage`,
			want: `container_cpu_usage{` + nrv + `}`,
		},
		{
			name: "existing selector gains the matcher",
			in:   `kube_pod_container_resource_limits{namespace="nudgebee",resource="cpu"}`,
			want: `kube_pod_container_resource_limits{namespace="nudgebee",resource="cpu",` + nrv + `}`,
		},
		{
			name: "empty selector left by the cluster placeholder",
			in:   `container_cpu_usage{}`,
			want: `container_cpu_usage{` + nrv + `}`,
		},
		{
			name: "range selector is not mistaken for a label set",
			in:   `rate(container_http_requests_total{path="/x"}[5m])`,
			want: `rate(container_http_requests_total{path="/x",` + nrv + `}[5m])`,
		},
		{
			name: "bare metric with a range selector",
			in:   `rate(container_http_requests_total[5m])`,
			want: `rate(container_http_requests_total{` + nrv + `}[5m])`,
		},
		{
			name: "function names and by-clause labels are untouched",
			in:   `sum by (namespace, pod) (max_over_time(container_cpu_usage{pod="p"}[30m:75s]))`,
			want: `sum by (namespace, pod) (max_over_time(container_cpu_usage{pod="p",` + nrv + `}[30m:75s]))`,
		},
		{
			// The OOM-kill key joins two selectors. Patching only the first would leave
			// the marker in the other half of the same answer.
			name: "every selector is rewritten, not just the first",
			in: `max by (namespace, container, pod) (kube_pod_container_resource_limits{resource="memory"}` +
				` * on (namespace, container, pod) group_left(reason) ` +
				`kube_pod_container_status_last_terminated_reason{reason="OOMKilled"})`,
			want: `max by (namespace, container, pod) (kube_pod_container_resource_limits{resource="memory",` + nrv + `}` +
				` * on (namespace, container, pod) group_left(reason) ` +
				`kube_pod_container_status_last_terminated_reason{reason="OOMKilled",` + nrv + `})`,
		},
		{
			name: "both operands of an or chain",
			in:   `node_memory_active_bytes or container_memory_usage`,
			want: `node_memory_active_bytes{` + nrv + `} or container_memory_usage{` + nrv + `}`,
		},
		{
			name: "scalar literals are not selectors",
			in:   `sum(container_cpu_usage) or vector(0)`,
			want: `sum(container_cpu_usage{` + nrv + `}) or vector(0)`,
		},
		{
			// `1e-06` and `5m` scan as identifiers if the previous character is ignored.
			name: "numeric literals and durations survive",
			in:   `avg_over_time(container_cpu_usage[5m]) > 1e-06`,
			want: `avg_over_time(container_cpu_usage{` + nrv + `}[5m]) > 1e-06`,
		},
		{
			name: "a label value containing braces is copied verbatim",
			in:   `container_cpu_usage{path="/a{b}c"}`,
			want: `container_cpu_usage{path="/a{b}c",` + nrv + `}`,
		},
		{
			name: "selector with no metric name",
			in:   `{__name__="container_cpu_usage"}`,
			want: `{__name__="container_cpu_usage",` + nrv + `}`,
		},
		{
			name: "offset keyword",
			in:   `container_cpu_usage offset 5m`,
			want: `container_cpu_usage{` + nrv + `} offset 5m`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := excludeOpenObserveNoRecordedValue(tc.in); got != tc.want {
				t.Errorf("\n in   %s\n got  %s\n want %s", tc.in, got, tc.want)
			}
		})
	}
}

// `group_left` and `group_right` take an OPTIONAL include list. Arming the label-list
// skip on the keyword alone consumed the next unrelated `(` -- a later sum() or rate() --
// and copied it verbatim, so every selector inside it silently kept its markers. Valid
// output, no error, wrong answer; and stable under repetition, so the idempotence test
// gave false confidence. Reachable through user-written PromQL (SupportsRawQuery).
func TestExcludeOpenObserveNoRecordedValueBareGroupModifier(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "group_left with no include list",
			in:   `a{x="1"} * on (pod) group_left b{y="2"} + sum(c{z="3"})`,
			want: `a{x="1",` + nrv + `} * on (pod) group_left b{y="2",` + nrv + `} + sum(c{z="3",` + nrv + `})`,
		},
		{
			name: "group_right with no include list",
			in:   `a{x="1"} * on (instance) group_right b{y="2"} + rate(c{z="3"}[5m])`,
			want: `a{x="1",` + nrv + `} * on (instance) group_right b{y="2",` + nrv + `} + rate(c{z="3",` + nrv + `}[5m])`,
		},
		{
			name: "group_left WITH an include list still skips that list",
			in:   `a{x="1"} * on (pod) group_left(reason) b{y="2"} + sum(c{z="3"})`,
			want: `a{x="1",` + nrv + `} * on (pod) group_left(reason) b{y="2",` + nrv + `} + sum(c{z="3",` + nrv + `})`,
		},
		{
			name: "bare group_left immediately before a function",
			in:   `group_left sum(c{z="3"})`,
			want: `group_left sum(c{z="3",` + nrv + `})`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := excludeOpenObserveNoRecordedValue(tc.in); got != tc.want {
				t.Errorf("\n in   %s\n got  %s\n want %s", tc.in, got, tc.want)
			}
		})
	}
}

// Running twice must not add the matcher twice, because a caller that re-wraps an expression
// (the fallback rounds re-ask the same key) would otherwise grow it on every pass.
func TestExcludeOpenObserveNoRecordedValueIsIdempotent(t *testing.T) {
	for _, in := range []string{
		`container_cpu_usage`,
		`kube_pod_container_resource_limits{resource="cpu"}`,
		`rate(container_http_requests_total[5m])`,
		`sum(a{x="1"}) / sum(b{y="2"})`,
	} {
		once := excludeOpenObserveNoRecordedValue(in)
		twice := excludeOpenObserveNoRecordedValue(once)
		if once != twice {
			t.Errorf("not idempotent for %q:\n once  %s\n twice %s", in, once, twice)
		}
	}
}

// An explicit flag matcher is the caller's decision and must be preserved, including the
// positive form used when someone deliberately inspects the markers.
func TestExcludeOpenObserveNoRecordedValueRespectsExplicitFlag(t *testing.T) {
	cases := []string{
		`kube_pod_container_resource_limits{flag="DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK"}`,
		`kube_pod_container_resource_limits{resource="cpu",flag=~"DATA_POINT.*"}`,
	}
	for _, in := range cases {
		if got := excludeOpenObserveNoRecordedValue(in); got != in {
			t.Errorf("explicit flag matcher altered:\n in  %s\n got %s", in, got)
		}
	}
}

// A label whose NAME merely contains "flag" does not constrain the flag label, so the
// exclusion must still be added.
func TestExcludeOpenObserveNoRecordedValueFlagLookalikes(t *testing.T) {
	in := `some_metric{flagged_total="1",other="flag"}`
	want := `some_metric{flagged_total="1",other="flag",` + nrv + `}`
	if got := excludeOpenObserveNoRecordedValue(in); got != want {
		t.Errorf("\n in   %s\n got  %s\n want %s", in, got, want)
	}
}

// An unterminated selector is left alone so OpenObserve reports the parse error, rather than
// this function turning a malformed query into a differently malformed one.
func TestExcludeOpenObserveNoRecordedValueLeavesMalformedAlone(t *testing.T) {
	in := `container_cpu_usage{namespace="x"`
	if got := excludeOpenObserveNoRecordedValue(in); got != in {
		t.Errorf("malformed expression rewritten:\n in  %s\n got %s", in, got)
	}
}
