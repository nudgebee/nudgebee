package observability

import (
	"errors"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
	"testing"
	"time"
)

// fakeAggregateSource is a stubLogSource whose backend counts: it maps each group_by
// name through fields, records the query it was asked, and answers rows.
type fakeAggregateSource struct {
	stubLogSource
	fields     map[string]string
	resolveErr error
	rows       LogAggregateRows
	aggErr     error
	got        LogAggregateQuery
}

func (f *fakeAggregateSource) ResolveGroupByFields(_ *security.RequestContext, _ LogAggregateQuery, names []string) ([]string, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = name
		if field, ok := f.fields[name]; ok {
			out[i] = field
		}
	}
	return out, nil
}

func (f *fakeAggregateSource) AggregateLogs(_ *security.RequestContext, q LogAggregateQuery) (LogAggregateRows, error) {
	f.got = q
	return f.rows, f.aggErr
}

// 2026-10-22 09:07:00 and 10:03:00 UTC, in unix ms.
var (
	aggTestStart = time.Date(2026, 10, 22, 9, 7, 0, 0, time.UTC).UnixMilli()
	aggTestEnd   = time.Date(2026, 10, 22, 10, 3, 0, 0, time.UTC).UnixMilli()
)

func aggTestRequest() FetchLogAggregateRequest {
	return FetchLogAggregateRequest{
		AccountId:   "acct",
		LogProvider: "test",
		StartTime:   aggTestStart,
		EndTime:     aggTestEnd,
	}
}

func podSeries(pod string, values ...float64) Result {
	ts := make([]int64, len(values))
	for i := range values {
		ts[i] = int64(i * 600)
	}
	return Result{Metric: map[string]string{"kubernetes.pod_name": pod}, Timestamps: ts, Values: values}
}

func TestValidateLogAggregateRequest(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(*FetchLogAggregateRequest)
		wantErr string
	}{
		{"no window", func(r *FetchLogAggregateRequest) { r.StartTime, r.EndTime = 0, 0 }, "start_time and end_time"},
		{"end before start", func(r *FetchLogAggregateRequest) { r.EndTime = r.StartTime - 1 }, "start_time and end_time"},
		{"four group_by", func(r *FetchLogAggregateRequest) { r.GroupBy = []string{"a", "b", "c", "d"} }, "at most 3"},
		{"empty name", func(r *FetchLogAggregateRequest) { r.GroupBy = []string{"pod", " "} }, "empty label name"},
		{"duplicate name", func(r *FetchLogAggregateRequest) { r.GroupBy = []string{"pod", "pod"} }, `"pod" twice`},
		{"bad interval", func(r *FetchLogAggregateRequest) { r.Interval = "hourly" }, "want a duration"},
		{"interval under a second", func(r *FetchLogAggregateRequest) { r.Interval = "500ms" }, "at least 1s"},
		{"interval over a day", func(r *FetchLogAggregateRequest) { r.Interval = "2d" }, "at most 1d"},
		{"interval over a day in hours", func(r *FetchLogAggregateRequest) { r.Interval = "36h" }, "at most 1d"},
		{"infinite interval", func(r *FetchLogAggregateRequest) { r.Interval = "Infd" }, "want a duration"},
		{"overflowing interval", func(r *FetchLogAggregateRequest) { r.Interval = "1e9d" }, "at most 1d"},
		{"interval longer than the window", func(r *FetchLogAggregateRequest) { r.Interval = "3h" }, "longer than the window"},
		{"window over 1000 days", func(r *FetchLogAggregateRequest) {
			r.StartTime = r.EndTime - (1001 * 24 * time.Hour).Milliseconds()
		}, "at most 1000 days"},
		{"end_time past year 3000", func(r *FetchLogAggregateRequest) { r.EndTime = 1<<63 - 1 }, "start_time and end_time"},
		{"window of centuries does not overflow past the cap", func(r *FetchLogAggregateRequest) {
			r.StartTime = 1
			r.EndTime = time.Date(2999, 12, 31, 0, 0, 0, 0, time.UTC).UnixMilli()
		}, "at most 1000 days"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := aggTestRequest()
			tc.edit(&req)
			_, _, err := validateLogAggregateRequest(req)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}

	t.Run("limit defaults and caps", func(t *testing.T) {
		for _, tc := range []struct{ in, want int }{{0, 100}, {-5, 100}, {25, 25}, {5000, 1000}} {
			req := aggTestRequest()
			req.Limit = tc.in
			limit, _, err := validateLogAggregateRequest(req)
			if err != nil || limit != tc.want {
				t.Errorf("limit %d → %d, %v; want %d", tc.in, limit, err, tc.want)
			}
		}
	})
}

func TestParseAndFormatLogAggregateInterval(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
		out  string
	}{
		{"", 0, ""},
		{"30s", 30 * time.Second, "30s"},
		{"90s", 90 * time.Second, "90s"},
		{"10m", 10 * time.Minute, "10m"},
		{"1h", time.Hour, "1h"},
		{"1d", 24 * time.Hour, "1d"},
		{"0.5d", 12 * time.Hour, "12h"},
	} {
		got, err := parseLogAggregateInterval(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("parse %q = %v, %v; want %v", tc.in, got, err, tc.want)
			continue
		}
		if got > 0 {
			if s := formatLogAggregateInterval(got); s != tc.out {
				t.Errorf("format %v = %q, want %q", got, s, tc.out)
			}
		}
	}
}

func TestFitLogAggregateWindow(t *testing.T) {
	t.Run("no interval counts the window as asked", func(t *testing.T) {
		start, end, interval, err := fitLogAggregateWindow(aggTestStart, aggTestEnd, 0)
		if err != nil || start != aggTestStart || end != aggTestEnd || interval != 0 {
			t.Fatalf("got %d %d %v %v", start, end, interval, err)
		}
	})

	t.Run("window widened to whole buckets", func(t *testing.T) {
		start, end, interval, err := fitLogAggregateWindow(aggTestStart, aggTestEnd, 10*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		wantStart := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC).UnixMilli()
		wantEnd := time.Date(2026, 10, 22, 10, 10, 0, 0, time.UTC).UnixMilli()
		if start != wantStart || end != wantEnd || interval != 10*time.Minute {
			t.Fatalf("got %s–%s @%v, want 09:00–10:10 @10m",
				time.UnixMilli(start).UTC().Format("15:04"), time.UnixMilli(end).UTC().Format("15:04"), interval)
		}
	})

	t.Run("aligned window is unchanged", func(t *testing.T) {
		s := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC).UnixMilli()
		e := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC).UnixMilli()
		start, end, _, err := fitLogAggregateWindow(s, e, time.Hour)
		if err != nil || start != s || end != e {
			t.Fatalf("got %d–%d, %v; want %d–%d", start, end, err, s, e)
		}
	})

	t.Run("too many buckets widens the interval", func(t *testing.T) {
		// 1 day at 1m is 1,440 buckets; 5m is the next step that fits.
		s := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC).UnixMilli()
		e := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC).UnixMilli()
		_, _, interval, err := fitLogAggregateWindow(s, e, time.Minute)
		if err != nil || interval != 5*time.Minute {
			t.Fatalf("interval = %v, %v; want 5m", interval, err)
		}
	})

	t.Run("a window no step fits is refused", func(t *testing.T) {
		e := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC).UnixMilli()
		s := e - (1001 * 24 * time.Hour).Milliseconds()
		if _, _, _, err := fitLogAggregateWindow(s, e, time.Hour); err == nil || !strings.Contains(err.Error(), "too long") {
			t.Fatalf("err = %v, want a too-long refusal", err)
		}
	})
}

func TestFetchLogAggregate_RefusesSourceThatCannotCount(t *testing.T) {
	_, err := fetchLogAggregateFrom(newLogLabelCtx(), &stubLogSource{}, aggTestRequest())
	if err == nil || !strings.Contains(err.Error(), "counting is not supported for") || !strings.Contains(err.Error(), "use logs_list") {
		t.Fatalf("err = %v, want the unsupported-provider refusal", err)
	}
}

func TestFetchLogAggregate_ShapesGroups(t *testing.T) {
	src := &fakeAggregateSource{
		fields: map[string]string{"pod": "kubernetes.pod_name"},
		rows: LogAggregateRows{
			Groups: []Result{
				podSeries("worker", 300, 400),
				podSeries("api", 1000, 1100),
				podSeries("cron", 5, 0),
			},
			Total:     2900,
			Ungrouped: 95,
			Complete:  true,
			Query:     "native count",
		},
	}
	req := aggTestRequest()
	req.GroupBy = []string{"pod"}
	req.Interval = "10m"
	req.Limit = 2

	res, err := fetchLogAggregateFrom(newLogLabelCtx(), src, req)
	if err != nil {
		t.Fatal(err)
	}

	// The source is asked in its own field names, over the aligned window.
	if len(src.got.GroupBy) != 1 || src.got.GroupBy[0] != "kubernetes.pod_name" {
		t.Errorf("source GroupBy = %v, want [kubernetes.pod_name]", src.got.GroupBy)
	}
	wantStart := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC).UnixMilli()
	if src.got.Request.StartTime != wantStart || res.StartTime != wantStart {
		t.Errorf("start = %d (source) / %d (result), want %d", src.got.Request.StartTime, res.StartTime, wantStart)
	}
	if src.got.Limit != 2 || src.got.Interval != 10*time.Minute {
		t.Errorf("source limit/interval = %d/%v, want 2/10m", src.got.Limit, src.got.Interval)
	}

	// Largest first, cut at the limit, labelled with the caller's name.
	if len(res.Series) != 2 || res.Series[0].Metric["pod"] != "api" || res.Series[1].Metric["pod"] != "worker" {
		t.Fatalf("series = %+v, want api then worker under label pod", res.Series)
	}
	if _, leaked := res.Series[0].Metric["kubernetes.pod_name"]; leaked {
		t.Errorf("provider field leaked into labels: %v", res.Series[0].Metric)
	}
	if res.GroupsTotal != 3 || res.GroupsOmitted != 1 {
		t.Errorf("groups total/omitted = %d/%d, want 3/1", res.GroupsTotal, res.GroupsOmitted)
	}
	if res.Total != 2900 || res.Ungrouped != 95 || !res.Complete {
		t.Errorf("total/ungrouped/complete = %d/%d/%v", res.Total, res.Ungrouped, res.Complete)
	}
	if res.Interval != "10m" || res.Query != "native count" || res.Provider != "test" || res.SeriesNote != "" {
		t.Errorf("interval/query/provider/note = %q/%q/%q/%q", res.Interval, res.Query, res.Provider, res.SeriesNote)
	}
}

func TestFetchLogAggregate_GroupsTotalFromSource(t *testing.T) {
	// A source that caps groups in the backend (Loki topk) returns limit groups and
	// says how many it found.
	src := &fakeAggregateSource{rows: LogAggregateRows{
		Groups:      []Result{podSeries("api", 10), podSeries("worker", 5)},
		Total:       40,
		GroupsTotal: 7,
		Complete:    true,
	}}
	req := aggTestRequest()
	req.GroupBy = []string{"kubernetes.pod_name"}
	req.Limit = 2
	res, err := fetchLogAggregateFrom(newLogLabelCtx(), src, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.GroupsTotal != 7 || res.GroupsOmitted != 5 {
		t.Errorf("groups total/omitted = %d/%d, want 7/5", res.GroupsTotal, res.GroupsOmitted)
	}
}

func TestFetchLogAggregate_NotesIncompleteAndWidened(t *testing.T) {
	src := &fakeAggregateSource{rows: LogAggregateRows{Groups: []Result{podSeries("api", 1)}, Total: 1}}
	req := aggTestRequest()
	req.StartTime = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC).UnixMilli()
	req.EndTime = time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC).UnixMilli()
	req.Interval = "1m"
	res, err := fetchLogAggregateFrom(newLogLabelCtx(), src, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete || res.Interval != "5m" {
		t.Errorf("complete/interval = %v/%q, want false/5m", res.Complete, res.Interval)
	}
	if !strings.Contains(res.SeriesNote, "widened from 1m to 5m") || !strings.Contains(res.SeriesNote, "lower bound") {
		t.Errorf("series_note = %q, want both the widening and the lower-bound notes", res.SeriesNote)
	}
}

func TestFetchLogAggregate_GroupByErrors(t *testing.T) {
	t.Run("source refusal is returned", func(t *testing.T) {
		src := &fakeAggregateSource{resolveErr: errors.New(`cannot group by "level": not a stream label`)}
		req := aggTestRequest()
		req.GroupBy = []string{"level"}
		if _, err := fetchLogAggregateFrom(newLogLabelCtx(), src, req); err == nil || !strings.Contains(err.Error(), "not a stream label") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("two names on one field", func(t *testing.T) {
		src := &fakeAggregateSource{fields: map[string]string{"pod": "k8s.pod.name", "pod_name": "k8s.pod.name"}}
		req := aggTestRequest()
		req.GroupBy = []string{"pod", "pod_name"}
		if _, err := fetchLogAggregateFrom(newLogLabelCtx(), src, req); err == nil || !strings.Contains(err.Error(), "same field") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestFetchLogAggregate_ZeroIsDiagnosed(t *testing.T) {
	t.Run("unknown label name gives a suggestion", func(t *testing.T) {
		src := &fakeAggregateSource{rows: LogAggregateRows{Total: 0, Complete: true}}
		src.labels = []OutputLogLabel{{Label: "namespace"}, {Label: "pod"}}
		req := aggTestRequest()
		req.QueryRequest.Where = query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespacee": {query.Eq: "prod"}}}
		res, err := fetchLogAggregateFrom(newLogLabelCtx(), src, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Suggestion == "" || !strings.Contains(res.Suggestion, "namespacee") {
			t.Fatalf("suggestion = %q, want one naming the unknown label", res.Suggestion)
		}
		if res.Total != 0 || res.Series == nil {
			t.Errorf("total/series = %d/%v, want 0 and an empty list", res.Total, res.Series)
		}
	})

	t.Run("a genuine zero comes back as zero", func(t *testing.T) {
		src := &fakeAggregateSource{rows: LogAggregateRows{Total: 0, Complete: true}}
		src.labels = []OutputLogLabel{{Label: "namespace"}}
		req := aggTestRequest()
		req.QueryRequest.Where = query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "prod"}}}
		res, err := fetchLogAggregateFrom(newLogLabelCtx(), src, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Suggestion != "" || res.Total != 0 || len(res.Series) != 0 {
			t.Errorf("got %+v, want a plain zero", res)
		}
	})

	t.Run("backend error with nothing to blame is returned", func(t *testing.T) {
		src := &fakeAggregateSource{aggErr: errors.New("maximum of series (500) reached")}
		if _, err := fetchLogAggregateFrom(newLogLabelCtx(), src, aggTestRequest()); err == nil || !strings.Contains(err.Error(), "maximum of series") {
			t.Fatalf("err = %v", err)
		}
	})
}
