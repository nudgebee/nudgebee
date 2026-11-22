package observability

import (
	"errors"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
	"testing"
	"time"
)

// fakeCountSource is a stubLogSource whose backend counts: it maps each group_by name
// through fields, plans one query naming the request, records what it was asked, and
// answers rows.
type fakeCountSource struct {
	stubLogSource
	fields     map[string]string
	resolveErr error
	rows       LogCountRows
	runErr     error
	got        LogCountQuery
	ran        bool
}

func (f *fakeCountSource) ResolveGroupByFields(_ *security.RequestContext, _ LogCountQuery, names []string) ([]string, error) {
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

func (f *fakeCountSource) PlanLogCount(_ *security.RequestContext, q LogCountQuery) (LogCountPlan, error) {
	return LogCountPlan{Queries: []string{"count by " + strings.Join(q.GroupBy, ",")}}, nil
}

func (f *fakeCountSource) RunLogCount(_ *security.RequestContext, q LogCountQuery, _ LogCountPlan) (LogCountRows, error) {
	f.got = q
	f.ran = true
	return f.rows, f.runErr
}

// 2026-10-22 09:07:00 and 10:03:00 UTC, in unix ms.
var (
	countTestStart = time.Date(2026, 10, 22, 9, 7, 0, 0, time.UTC).UnixMilli()
	countTestEnd   = time.Date(2026, 10, 22, 10, 3, 0, 0, time.UTC).UnixMilli()
)

func countTestRequest(count LogCountRequest) FetchLogRequest {
	return FetchLogRequest{
		AccountId:    "acct",
		LogProvider:  "test",
		StartTime:    countTestStart,
		EndTime:      countTestEnd,
		QueryRequest: LogsQueryBuilderRequest{Count: &count},
	}
}

func podSeries(pod string, values ...float64) Result {
	ts := make([]int64, len(values))
	for i := range values {
		ts[i] = int64(i * 600)
	}
	return Result{Metric: map[string]string{"kubernetes.pod_name": pod}, Timestamps: ts, Values: values}
}

func TestLogsQueryBuilderRequest_EmptyCountTurnsOnCountMode(t *testing.T) {
	var req FetchLogRequest
	in := map[string]any{"account_id": "a", "query_request": map[string]any{"where": map[string]any{}, "count": map[string]any{}}}
	if err := common.UnmarshalMapToStruct(in, &req); err != nil {
		t.Fatal(err)
	}
	if req.QueryRequest.Count == nil {
		t.Fatal(`"count": {} decoded to nil; an empty count must still ask for count mode`)
	}

	var lines FetchLogRequest
	if err := common.UnmarshalMapToStruct(map[string]any{"query_request": map[string]any{"where": map[string]any{}}}, &lines); err != nil {
		t.Fatal(err)
	}
	if lines.QueryRequest.Count != nil {
		t.Fatal("a request without count must stay in lines mode")
	}
}

func TestValidateLogCountRequest(t *testing.T) {
	cases := []struct {
		name       string
		start, end int64
		count      LogCountRequest
		wantErr    string
	}{
		{"no window", 0, 0, LogCountRequest{}, "start_time and end_time"},
		{"end before start", countTestStart, countTestStart - 1, LogCountRequest{}, "start_time and end_time"},
		{"four group_by", countTestStart, countTestEnd, LogCountRequest{GroupBy: []string{"a", "b", "c", "d"}}, "at most 3"},
		{"empty name", countTestStart, countTestEnd, LogCountRequest{GroupBy: []string{"pod", " "}}, "empty label name"},
		{"duplicate name", countTestStart, countTestEnd, LogCountRequest{GroupBy: []string{"pod", "pod"}}, `"pod" twice`},
		{"bad interval", countTestStart, countTestEnd, LogCountRequest{Interval: "hourly"}, "want a duration"},
		{"interval under a second", countTestStart, countTestEnd, LogCountRequest{Interval: "500ms"}, "at least 1s"},
		{"interval over a day", countTestStart, countTestEnd, LogCountRequest{Interval: "2d"}, "at most 1d"},
		{"interval over a day in hours", countTestStart, countTestEnd, LogCountRequest{Interval: "36h"}, "at most 1d"},
		{"infinite interval", countTestStart, countTestEnd, LogCountRequest{Interval: "Infd"}, "want a duration"},
		{"overflowing interval", countTestStart, countTestEnd, LogCountRequest{Interval: "1e9d"}, "at most 1d"},
		{"interval longer than the window", countTestStart, countTestEnd, LogCountRequest{Interval: "3h"}, "longer than the window"},
		{"window over 1000 days", countTestEnd - (1001 * 24 * time.Hour).Milliseconds(), countTestEnd, LogCountRequest{}, "at most 1000 days"},
		{"end_time past year 3000", countTestStart, 1<<63 - 1, LogCountRequest{}, "start_time and end_time"},
		{"window of centuries does not overflow past the cap", 1, time.Date(2999, 12, 31, 0, 0, 0, 0, time.UTC).UnixMilli(), LogCountRequest{}, "at most 1000 days"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := validateLogCountRequest(tc.start, tc.end, tc.count)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}

	t.Run("limit defaults and caps", func(t *testing.T) {
		for _, tc := range []struct{ in, want int }{{0, 100}, {-5, 100}, {25, 25}, {5000, 1000}} {
			limit, _, err := validateLogCountRequest(countTestStart, countTestEnd, LogCountRequest{Limit: tc.in})
			if err != nil || limit != tc.want {
				t.Errorf("limit %d → %d, %v; want %d", tc.in, limit, err, tc.want)
			}
		}
	})
}

func TestRefuseLineOnlyFields(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(*FetchLogRequest)
		wantErr string
	}{
		{"hand-written query", func(r *FetchLogRequest) { r.Query = `{"aggs":{}}` }, "send one or the other"},
		{"line limit", func(r *FetchLogRequest) { r.Limit = 1000 }, "query_request.count.limit"},
		{"offset", func(r *FetchLogRequest) { r.Offset = 10 }, "offset"},
		{"sort", func(r *FetchLogRequest) { r.SortFields = []SortField{{ColumnName: "ts"}} }, "largest first"},
		{"step_interval", func(r *FetchLogRequest) { r.StepInterval = 600 }, "query_request.count.interval"},
		{"include_raw", func(r *FetchLogRequest) { r.IncludeRaw = true }, "include_raw"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := countTestRequest(LogCountRequest{})
			tc.edit(&req)
			src := &fakeCountSource{}
			_, err := fetchLogCount(newLogLabelCtx(), src, req)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
			if src.ran {
				t.Error("the count ran although the request was refused")
			}
		})
	}
}

func TestParseAndFormatLogCountInterval(t *testing.T) {
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
		got, err := parseLogCountInterval(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("parse %q = %v, %v; want %v", tc.in, got, err, tc.want)
			continue
		}
		if got > 0 {
			if s := formatLogCountInterval(got); s != tc.out {
				t.Errorf("format %v = %q, want %q", got, s, tc.out)
			}
		}
	}
}

func TestFitLogCountWindow(t *testing.T) {
	t.Run("no interval counts the window as asked", func(t *testing.T) {
		start, end, interval, err := fitLogCountWindow(countTestStart, countTestEnd, 0)
		if err != nil || start != countTestStart || end != countTestEnd || interval != 0 {
			t.Fatalf("got %d %d %v %v", start, end, interval, err)
		}
	})

	t.Run("window widened to whole buckets", func(t *testing.T) {
		start, end, interval, err := fitLogCountWindow(countTestStart, countTestEnd, 10*time.Minute)
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
		start, end, _, err := fitLogCountWindow(s, e, time.Hour)
		if err != nil || start != s || end != e {
			t.Fatalf("got %d–%d, %v; want %d–%d", start, end, err, s, e)
		}
	})

	t.Run("too many buckets widens the interval", func(t *testing.T) {
		// 1 day at 1m is 1,440 buckets; 5m is the next step that fits.
		s := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC).UnixMilli()
		e := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC).UnixMilli()
		_, _, interval, err := fitLogCountWindow(s, e, time.Minute)
		if err != nil || interval != 5*time.Minute {
			t.Fatalf("interval = %v, %v; want 5m", interval, err)
		}
	})

	t.Run("a window no step fits is refused", func(t *testing.T) {
		e := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC).UnixMilli()
		s := e - (1001 * 24 * time.Hour).Milliseconds()
		if _, _, _, err := fitLogCountWindow(s, e, time.Hour); err == nil || !strings.Contains(err.Error(), "too long") {
			t.Fatalf("err = %v, want a too-long refusal", err)
		}
	})
}

func TestFillLogCountBuckets(t *testing.T) {
	start := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC).UnixMilli()
	end := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC).UnixMilli()
	s := start / 1000
	got := fillLogCountBuckets(Result{
		Metric:     map[string]string{"pod": "api"},
		Timestamps: []int64{s + 1200, s},
		Values:     []float64{5, 3},
	}, start, end, 20*time.Minute)
	if len(got.Timestamps) != 3 || got.Timestamps[0] != s || got.Timestamps[2] != s+2400 {
		t.Fatalf("timestamps = %v, want 09:00, 09:20, 09:40", got.Timestamps)
	}
	// s+1200 is 09:20, the second bucket; 09:40 counted nothing.
	if got.Values[0] != 3 || got.Values[1] != 5 || got.Values[2] != 0 {
		t.Fatalf("values = %v, want [3 5 0]", got.Values)
	}
	if got.Metric["pod"] != "api" {
		t.Errorf("labels lost: %v", got.Metric)
	}

	// A point stamped inside a bucket (09:25, 09:47) lands in that bucket, never dropped.
	off := fillLogCountBuckets(Result{Timestamps: []int64{s + 1500, s + 2820}, Values: []float64{4, 6}}, start, end, 20*time.Minute)
	if off.Values[0] != 0 || off.Values[1] != 4 || off.Values[2] != 6 {
		t.Fatalf("off-grid values = %v, want [0 4 6]", off.Values)
	}
}

func TestFetchLogCount_RefusesSourceThatCannotCount(t *testing.T) {
	_, err := fetchLogCount(newLogLabelCtx(), &stubLogSource{}, countTestRequest(LogCountRequest{}))
	if err == nil || !strings.Contains(err.Error(), "counting is not supported for") || !strings.Contains(err.Error(), "drop query_request.count") {
		t.Fatalf("err = %v, want the unsupported-provider refusal", err)
	}
}

func TestFetchLogCount_ShapesGroups(t *testing.T) {
	src := &fakeCountSource{
		fields: map[string]string{"pod": "kubernetes.pod_name"},
		rows: LogCountRows{
			Groups: []Result{
				podSeries("worker", 300, 400),
				podSeries("api", 1000, 1100),
				podSeries("cron", 5, 0),
			},
			Total:     2900,
			Ungrouped: 95,
			Complete:  true,
		},
	}
	req := countTestRequest(LogCountRequest{GroupBy: []string{"pod"}, Interval: "10m", Limit: 2})

	res, err := fetchLogCount(newLogLabelCtx(), src, req)
	if err != nil {
		t.Fatal(err)
	}

	// The source is asked in its own field names, over the aligned window.
	if len(src.got.GroupBy) != 1 || src.got.GroupBy[0] != "kubernetes.pod_name" {
		t.Errorf("source GroupBy = %v, want [kubernetes.pod_name]", src.got.GroupBy)
	}
	wantStart := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC).UnixMilli()
	if src.got.Request.StartTime != wantStart || res.Count.StartTime != wantStart {
		t.Errorf("start = %d (source) / %d (result), want %d", src.got.Request.StartTime, res.Count.StartTime, wantStart)
	}
	if src.got.Limit != 2 || src.got.Interval != 10*time.Minute {
		t.Errorf("source limit/interval = %d/%v, want 2/10m", src.got.Limit, src.got.Interval)
	}

	// Count mode is self-describing: mode, an exact total, count details.
	if res.Mode != LogResultModeCount || res.Total == nil || *res.Total != 2900 || res.TotalRelation != "eq" {
		t.Errorf("mode/total/relation = %q/%v/%q, want count/2900/eq", res.Mode, res.Total, res.TotalRelation)
	}
	if len(res.Logs) != 0 || res.Logs == nil {
		t.Errorf("logs = %v, want an empty list", res.Logs)
	}

	// Largest first, cut at the limit, labelled with the caller's name, one point per bucket.
	if len(res.Series) != 2 || res.Series[0].Metric["pod"] != "api" || res.Series[1].Metric["pod"] != "worker" {
		t.Fatalf("series = %+v, want api then worker under label pod", res.Series)
	}
	if _, leaked := res.Series[0].Metric["kubernetes.pod_name"]; leaked {
		t.Errorf("provider field leaked into labels: %v", res.Series[0].Metric)
	}
	if len(res.Series[0].Values) != 7 {
		t.Errorf("api has %d points, want 7 (09:00–10:10 in 10m buckets)", len(res.Series[0].Values))
	}
	c := res.Count
	if c.GroupsTotal != 3 || c.GroupsOmitted != 1 || !res.Truncated {
		t.Errorf("groups total/omitted/truncated = %d/%d/%v, want 3/1/true", c.GroupsTotal, c.GroupsOmitted, res.Truncated)
	}
	if c.Ungrouped != 95 || !c.Complete || c.Interval != "10m" {
		t.Errorf("ungrouped/complete/interval = %d/%v/%q", c.Ungrouped, c.Complete, c.Interval)
	}
	if res.Query != "count by kubernetes.pod_name" || res.Provider != "test" || res.SeriesNote != "" {
		t.Errorf("query/provider/note = %q/%q/%q", res.Query, res.Provider, res.SeriesNote)
	}
}

func TestFetchLogCount_GroupsTotalFromSource(t *testing.T) {
	// A source that caps groups in the backend returns limit groups and says how many it found.
	src := &fakeCountSource{rows: LogCountRows{
		Groups:      []Result{podSeries("api", 10), podSeries("worker", 5)},
		Total:       40,
		GroupsTotal: 7,
		Complete:    true,
	}}
	res, err := fetchLogCount(newLogLabelCtx(), src, countTestRequest(LogCountRequest{GroupBy: []string{"kubernetes.pod_name"}, Limit: 2}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Count.GroupsTotal != 7 || res.Count.GroupsOmitted != 5 {
		t.Errorf("groups total/omitted = %d/%d, want 7/5", res.Count.GroupsTotal, res.Count.GroupsOmitted)
	}
}

func TestFetchLogCount_NotesIncompleteAndWidened(t *testing.T) {
	src := &fakeCountSource{rows: LogCountRows{Groups: []Result{podSeries("api", 1)}, Total: 1}}
	req := countTestRequest(LogCountRequest{Interval: "1m"})
	req.StartTime = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC).UnixMilli()
	req.EndTime = time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC).UnixMilli()
	res, err := fetchLogCount(newLogLabelCtx(), src, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Count.Complete || res.Count.Interval != "5m" || !res.Truncated {
		t.Errorf("complete/interval/truncated = %v/%q/%v, want false/5m/true", res.Count.Complete, res.Count.Interval, res.Truncated)
	}
	if !strings.Contains(res.SeriesNote, "widened from 1m to 5m") || !strings.Contains(res.SeriesNote, "lower bound") {
		t.Errorf("series_note = %q, want both the widening and the lower-bound notes", res.SeriesNote)
	}
}

func TestFetchLogCount_GroupByErrors(t *testing.T) {
	t.Run("source refusal is returned", func(t *testing.T) {
		src := &fakeCountSource{resolveErr: errors.New(`cannot group by "level": not a stream label`)}
		if _, err := fetchLogCount(newLogLabelCtx(), src, countTestRequest(LogCountRequest{GroupBy: []string{"level"}})); err == nil || !strings.Contains(err.Error(), "not a stream label") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("two names on one field", func(t *testing.T) {
		src := &fakeCountSource{fields: map[string]string{"pod": "k8s.pod.name", "pod_name": "k8s.pod.name"}}
		if _, err := fetchLogCount(newLogLabelCtx(), src, countTestRequest(LogCountRequest{GroupBy: []string{"pod", "pod_name"}})); err == nil || !strings.Contains(err.Error(), "same field") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestFetchLogCount_ZeroIsDiagnosed(t *testing.T) {
	t.Run("unknown label name gives a suggestion, and the zero total", func(t *testing.T) {
		src := &fakeCountSource{rows: LogCountRows{Total: 0, Complete: true}}
		src.labels = []OutputLogLabel{{Label: "namespace"}, {Label: "pod"}}
		req := countTestRequest(LogCountRequest{})
		req.QueryRequest.Where = query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespacee": {query.Eq: "prod"}}}
		res, err := fetchLogCount(newLogLabelCtx(), src, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Suggestion == "" || !strings.Contains(res.Suggestion, "namespacee") {
			t.Fatalf("suggestion = %q, want one naming the unknown label", res.Suggestion)
		}
		if res.Mode != LogResultModeCount || res.Total == nil || *res.Total != 0 {
			t.Errorf("mode/total = %q/%v, want count and an explicit 0", res.Mode, res.Total)
		}
	})

	t.Run("a genuine zero comes back as an explicit zero", func(t *testing.T) {
		src := &fakeCountSource{rows: LogCountRows{Total: 0, Complete: true}}
		src.labels = []OutputLogLabel{{Label: "namespace"}}
		req := countTestRequest(LogCountRequest{})
		req.QueryRequest.Where = query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "prod"}}}
		res, err := fetchLogCount(newLogLabelCtx(), src, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Suggestion != "" || res.Total == nil || *res.Total != 0 || len(res.Series) != 0 || res.Truncated {
			t.Errorf("got %+v, want a plain zero", res)
		}
	})

	t.Run("backend error with nothing to blame is returned", func(t *testing.T) {
		src := &fakeCountSource{runErr: errors.New("maximum of series (500) reached")}
		if _, err := fetchLogCount(newLogLabelCtx(), src, countTestRequest(LogCountRequest{})); err == nil || !strings.Contains(err.Error(), "maximum of series") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPreviewLogCount(t *testing.T) {
	src := &fakeCountSource{fields: map[string]string{"pod": "kubernetes.pod_name"}}
	got, err := previewLogCount(newLogLabelCtx(), src, countTestRequest(LogCountRequest{GroupBy: []string{"pod"}}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Query != "count by kubernetes.pod_name" {
		t.Errorf("preview = %q, want the counting query", got.Query)
	}
	if src.ran {
		t.Error("the preview ran the count")
	}
}
