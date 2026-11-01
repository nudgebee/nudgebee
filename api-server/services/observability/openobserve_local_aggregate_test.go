package observability

import (
	"math"
	"testing"
)

func TestParseOpenObserveOverTime(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantOK    bool
		wantOp    string
		wantInner string
		wantStep  int
		wantPhi   float64
	}{
		{
			name: "exactly what promQLOverTime emits", wantOK: true,
			in:        `max_over_time((sum(kube_pod_container_resource_limits{resource="cpu"}))[86400s:75s])`,
			wantOp:    "max",
			wantInner: `sum(kube_pod_container_resource_limits{resource="cpu"})`,
			wantStep:  75,
		},
		{
			name: "avg", wantOK: true,
			in:        `avg_over_time((a{x="1"})[1d:5m])`,
			wantOp:    "avg",
			wantInner: `a{x="1"}`,
			wantStep:  300,
		},
		{
			name: "quantile carries its phi", wantOK: true,
			in:        `quantile_over_time(0.9, (a{x="1"})[1d:75s])`,
			wantOp:    "quantile",
			wantInner: `a{x="1"}`,
			wantStep:  75,
			wantPhi:   0.9,
		},
		{
			name: "count", wantOK: true,
			in: `count_over_time((a)[3h:30s])`, wantOp: "count", wantInner: "a", wantStep: 30,
		},
		// Not our wrapper: leave these for OpenObserve, whatever it does with them.
		{name: "no wrapper", in: `sum(a{x="1"})`},
		{name: "plain range selector, not a subquery", in: `rate(a[5m])`},
		{name: "wrapper is only part of the expression", in: `max_over_time((a)[1d:75s]) + 1`},
		{name: "unknown over_time variant", in: `last_over_time((a)[1d:75s])`},
		{name: "quantile without a phi", in: `quantile_over_time((a)[1d:75s])`},
		{name: "empty inner", in: `max_over_time(()[1d:75s])`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseOpenObserveOverTime(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (in %s)", ok, tc.wantOK, tc.in)
			}
			if !tc.wantOK {
				return
			}
			if got.Op != tc.wantOp || got.Inner != tc.wantInner || got.StepSecs != tc.wantStep {
				t.Errorf("\n in    %s\n got   op=%s inner=%q step=%d\n want  op=%s inner=%q step=%d",
					tc.in, got.Op, got.Inner, got.StepSecs, tc.wantOp, tc.wantInner, tc.wantStep)
			}
			if tc.wantPhi != 0 && got.Quantile != tc.wantPhi {
				t.Errorf("quantile=%v want %v", got.Quantile, tc.wantPhi)
			}
		})
	}
}

// The defect this exists for: max, min and avg must give DIFFERENT answers over a series
// that varies. OpenObserve returned one number for all three.
func TestReduceOpenObserveSeriesStatisticsDiffer(t *testing.T) {
	in := []Result{{
		Metric:     map[string]string{"pod": "p1"},
		Timestamps: []int64{1, 2, 3, 4},
		Values:     []float64{1, 2, 1, 2},
	}}
	got := map[string]float64{}
	for _, op := range []string{"max", "min", "avg", "sum", "count"} {
		out := reduceOpenObserveSeries(in, openObserveLocalAggregate{Op: op})
		if len(out) != 1 || len(out[0].Values) != 1 {
			t.Fatalf("%s: want exactly one series with one point, got %#v", op, out)
		}
		if out[0].Timestamps[0] != 4 {
			t.Errorf("%s: timestamp %d, want the series' last (4)", op, out[0].Timestamps[0])
		}
		got[op] = out[0].Values[0]
	}
	want := map[string]float64{"max": 2, "min": 1, "avg": 1.5, "sum": 6, "count": 4}
	for op, w := range want {
		if got[op] != w {
			t.Errorf("%s = %v, want %v", op, got[op], w)
		}
	}
	if got["max"] == got["avg"] {
		t.Error("max equals avg on a varying series -- the defect this fix exists for")
	}
}

// One point per SERIES, not one per workload: ~30 UI call sites read payload[0].
func TestReduceOpenObserveSeriesCollapsesEachSeries(t *testing.T) {
	in := []Result{
		{Metric: map[string]string{"pod": "a"}, Timestamps: []int64{1, 2}, Values: []float64{1, 5}},
		{Metric: map[string]string{"pod": "b"}, Timestamps: []int64{1, 2}, Values: []float64{2, 3}},
	}
	out := reduceOpenObserveSeries(in, openObserveLocalAggregate{Op: "max"})
	if len(out) != 2 {
		t.Fatalf("got %d series, want 2", len(out))
	}
	for _, s := range out {
		if len(s.Values) != 1 {
			t.Errorf("pod %s: %d points, want 1", s.Metric["pod"], len(s.Values))
		}
	}
}

// A series with no usable samples is dropped, never reported as zero: a caller cannot tell
// a zero from "used nothing", and a failed read once flagged a live workload as abandoned.
func TestReduceOpenObserveSeriesDropsEmptyRatherThanZero(t *testing.T) {
	nan := math.NaN()
	in := []Result{
		{Metric: map[string]string{"pod": "empty"}, Timestamps: []int64{}, Values: []float64{}},
		{Metric: map[string]string{"pod": "nan"}, Timestamps: []int64{1}, Values: []float64{nan}},
		{Metric: map[string]string{"pod": "ok"}, Timestamps: []int64{1}, Values: []float64{7}},
	}
	out := reduceOpenObserveSeries(in, openObserveLocalAggregate{Op: "max"})
	if len(out) != 1 || out[0].Metric["pod"] != "ok" {
		t.Fatalf("want only the series with a usable sample, got %#v", out)
	}
	// count is the exception: it reports how many readings there were, so an all-NaN
	// series legitimately counts zero rather than disappearing.
	cnt := reduceOpenObserveSeries(in[1:2], openObserveLocalAggregate{Op: "count"})
	if len(cnt) != 1 || cnt[0].Values[0] != 0 {
		t.Errorf("count over an all-NaN series: got %#v, want one series valued 0", cnt)
	}
}

// Indexing Timestamps on the strength of a Values check panics with index out of range [-1].
// Not reachable from this source's own decode, which fills both in lockstep, but Result is a
// shared type and a panic takes the whole request down.
func TestReduceOpenObserveSeriesSurvivesMismatchedSeries(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Result
	}{
		{"values without timestamps", Result{Values: []float64{1, 2}}},
		{"timestamps without values", Result{Timestamps: []int64{1, 2}}},
		{"fewer timestamps than values", Result{Timestamps: []int64{1}, Values: []float64{1, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Metric = map[string]string{"pod": "p"}
			for _, op := range []string{"max", "min", "avg", "sum", "count", "quantile"} {
				out := reduceOpenObserveSeries([]Result{tc.in},
					openObserveLocalAggregate{Op: op, Quantile: 0.9})
				for _, s := range out {
					if len(s.Values) != len(s.Timestamps) {
						t.Errorf("%s: emitted %d values against %d timestamps",
							op, len(s.Values), len(s.Timestamps))
					}
				}
			}
		})
	}
}

// A p90 computed here must match what Prometheus would answer for the same samples.
func TestReduceOpenObserveSeriesQuantileMatchesPromQL(t *testing.T) {
	in := []Result{{Metric: map[string]string{}, Timestamps: []int64{1, 2, 3, 4, 5},
		Values: []float64{1, 2, 3, 4, 5}}}
	for _, tc := range []struct{ phi, want float64 }{
		{0, 1}, {0.5, 3}, {1, 5}, {0.9, 4.6}, {0.25, 2},
	} {
		out := reduceOpenObserveSeries(in, openObserveLocalAggregate{Op: "quantile", Quantile: tc.phi})
		if len(out) != 1 {
			t.Fatalf("phi=%v: no series", tc.phi)
		}
		if math.Abs(out[0].Values[0]-tc.want) > 1e-9 {
			t.Errorf("phi=%v -> %v, want %v", tc.phi, out[0].Values[0], tc.want)
		}
	}
}
